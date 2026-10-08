package nixroots

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// registry.go is a workspace's MANAGED ROOTS (a term coined in
// docs/design/in-jail-nix-roots.md §4.1): links yolo owns, one per link a jail asked nix to
// root, each pointing at the same store path and registered with the host's daemon under the
// host's spelling. The user's own link (`/workspace/result`, a profile generation) is never
// touched; the managed link beside it is what the host's GC honors, and RELEASING a root is
// deleting that managed link, after which the next GC may collect the store path exactly as
// it would on a host whose `result` link was removed.
//
// Why a link of yolo's rather than registering the host spelling of the user's own link: the
// daemon has no operation that drops an indirect root, so a root registered at the user's
// link lives exactly as long as that link, with no lease, no cap and no release. The
// maintainer ruled all three mandatory (OQ-NR1), so the root must be a link yolo can delete.
//
// THE DIRECTORY IS JAIL-WRITABLE and the host reads and deletes in it (`yolo nix-roots
// release` on the host). Every access goes through an os.Root on a directory that is checked
// not to be a link (paths.OpenStateDirRoot), every name deleted is a fixed-width hex id, and
// the jail-written record is data: nothing in it names a path the host deletes.

// Layout under <workspace>/.yolo.
const (
	// RegistryDirName is the registry's directory under the workspace's .yolo.
	RegistryDirName = "nix-roots"
	registryFile    = "roots.json"
	registryLock    = ".lock"
	linksDirName    = "links"
)

// The lifecycle's numbers: implementation decisions NR-D3 and NR-D4 in the design's ledger,
// reversible, and constants rather than config so one place states them.
const (
	// DefaultLease is how long a managed root lives after its last renewal: a new
	// registration of the same link (a rebuild), or an explicit keep. A week covers a weekly
	// revisit and is the image roots' own age floor (OQ-LS1).
	DefaultLease = 7 * 24 * time.Hour
	// DefaultCap is the most managed roots one workspace holds; admitting past it releases
	// the least recently renewed. It is a COUNT, not bytes: it bounds how many distinct
	// closures a forgotten workspace can pin, not how large one is.
	DefaultCap = 64
)

// Ways a root was admitted, as Root.By records them.
const (
	ByWatch = "watch"
	ByKeep  = "keep"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Root is one managed root as the registry records it.
type Root struct {
	// ID names the managed link, links/<ID>: the first 16 hex digits of sha256(Source).
	ID string `json:"id"`
	// Source is the link nix was asked to root, as the jail spells it.
	Source string `json:"source"`
	// SourceHost is the same link as the host spells it, so a host-side prune can see it.
	SourceHost string `json:"source_host"`
	// Target is the store path both links point at.
	Target string `json:"target"`
	// LinkHost is the host's spelling of the managed link, the string the daemon holds.
	LinkHost string `json:"link_host"`
	// Admitted is when the root was first made; Renewed when it was last renewed.
	Admitted time.Time `json:"admitted"`
	Renewed  time.Time `json:"renewed"`
	// By is ByWatch or ByKeep.
	By string `json:"by"`
}

// Expires is when the root's lease ends.
func (r Root) Expires(lease time.Duration) time.Time { return r.Renewed.Add(lease) }

// RootID is the id a managed root for source gets.
func RootID(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])[:16]
}

type registryState struct {
	Version int    `json:"version"`
	Roots   []Root `json:"roots"`
}

// Released is one root a registry operation released, and why.
type Released struct {
	Root   Root
	Reason string
}

// Reasons a root is released.
const (
	ReasonExpired  = "lease expired"
	ReasonCap      = "over the workspace cap"
	ReasonGone     = "its link is gone"
	ReasonNotStore = "its link no longer points into the store"
	ReasonAsked    = "released"
)

// Registry is one workspace's managed roots.
type Registry struct {
	// Workspace is the workspace directory as THIS process sees it: /workspace in a jail, the
	// host path on the host.
	Workspace string
	// Lease and Cap are DefaultLease and DefaultCap when zero.
	Lease time.Duration
	Cap   int
	// Now is time.Now when nil.
	Now func() time.Time
	// Register sends the managed link (an absolute path as this process spells it) to the
	// host's daemon and returns the host's spelling. A jail sets it (Registrar.Register); the
	// host leaves it nil, because a link the host made is already spelled as the host
	// resolves it and the host's yolo makes no new roots here.
	Register func(link string) (string, error)
	// StoreDir is DefaultStoreDir when "".
	StoreDir string
	// SourceSide picks which spelling of a source this process can see: SideJail or SideHost.
	SourceSide Side
}

// Side says which filesystem the process runs in.
type Side int

const (
	SideJail Side = iota
	SideHost
)

func (g *Registry) lease() time.Duration {
	if g.Lease > 0 {
		return g.Lease
	}
	return DefaultLease
}

func (g *Registry) cap() int {
	if g.Cap > 0 {
		return g.Cap
	}
	return DefaultCap
}

func (g *Registry) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Registry) storeDir() string {
	if g.StoreDir != "" {
		return g.StoreDir
	}
	return DefaultStoreDir
}

// Dir is the registry's directory as this process spells it.
func (g *Registry) Dir() string {
	return filepath.Join(paths.WorkspaceStateDir(g.Workspace), RegistryDirName)
}

// LinkPath is a managed link's path as this process spells it.
func (g *Registry) LinkPath(id string) string { return filepath.Join(g.Dir(), linksDirName, id) }

// session is the registry open under its lock.
type session struct {
	g     *Registry
	dir   *os.Root
	links *os.Root
	lock  *os.File
	state registryState
}

func (s *session) close() {
	if s.lock != nil {
		_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		_ = s.lock.Close()
	}
	if s.links != nil {
		_ = s.links.Close()
	}
	if s.dir != nil {
		_ = s.dir.Close()
	}
}

// open opens the registry under an exclusive lock. create makes the directories when they
// are missing; without it a missing registry is fs.ErrNotExist.
func (g *Registry) open(create bool) (*session, error) {
	stateDir := paths.WorkspaceStateDir(g.Workspace)
	if create {
		if _, err := paths.EnsureWorkspaceStateDir(g.Workspace); err != nil {
			return nil, err
		}
	}
	state, err := paths.OpenStateDirRoot(stateDir)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	if create {
		if err := state.Mkdir(RegistryDirName, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	s := &session{g: g}
	if s.dir, err = paths.OpenStateSubdirRoot(state, RegistryDirName, g.Dir()); err != nil {
		return nil, err
	}
	if create {
		if err := s.dir.Mkdir(linksDirName, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			s.close()
			return nil, err
		}
	}
	if s.links, err = paths.OpenStateSubdirRoot(s.dir, linksDirName, filepath.Join(g.Dir(), linksDirName)); err != nil {
		if !create && errors.Is(err, fs.ErrNotExist) {
			s.links = nil
		} else {
			s.close()
			return nil, err
		}
	}
	if s.lock, err = s.dir.OpenFile(registryLock, os.O_RDWR|os.O_CREATE, 0o644); err != nil {
		s.close()
		return nil, err
	}
	if err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX); err != nil {
		s.close()
		return nil, err
	}
	if err := s.load(); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// load reads roots.json. A missing file is an empty registry. An unreadable or malformed
// one is an error naming the file: overwriting it would release every root it holds without
// deleting their links, and the links would then pin their closures with nothing to list them.
func (s *session) load() error {
	b, err := s.dir.ReadFile(registryFile)
	if errors.Is(err, fs.ErrNotExist) {
		s.state = registryState{Version: 1}
		return nil
	}
	if err != nil {
		return err
	}
	var st registryState
	if err := json.Unmarshal(b, &st); err != nil {
		return &CorruptError{Path: filepath.Join(s.g.Dir(), registryFile), Err: err}
	}
	var keep []Root
	for _, r := range st.Roots {
		if idPattern.MatchString(r.ID) {
			keep = append(keep, r)
		}
	}
	st.Roots = keep
	st.Version = 1
	s.state = st
	return nil
}

// CorruptError is a roots.json yolo cannot read.
type CorruptError struct {
	Path string
	Err  error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("%s is not a registry yolo can read (%v); move it aside and run "+
		"`yolo nix-roots prune`, which releases every managed link no record names", e.Path, e.Err)
}

func (s *session) save() error {
	sort.Slice(s.state.Roots, func(i, j int) bool { return s.state.Roots[i].ID < s.state.Roots[j].ID })
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := registryFile + ".tmp-" + strconv.Itoa(os.Getpid())
	if err := s.dir.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := s.dir.Rename(tmp, registryFile); err != nil {
		_ = s.dir.Remove(tmp)
		return err
	}
	return nil
}

// relink points links/<id> at target, atomically.
func (s *session) relink(id, target string) error {
	tmp := id + ".tmp-" + strconv.Itoa(os.Getpid())
	_ = s.links.Remove(tmp)
	if err := s.links.Symlink(target, tmp); err != nil {
		return err
	}
	if err := s.links.Rename(tmp, id); err != nil {
		_ = s.links.Remove(tmp)
		return err
	}
	return nil
}

// unlink removes links/<id>, when it is a symlink. Anything else at the name is left alone:
// it is not a link yolo made.
func (s *session) unlink(id string) error {
	if s.links == nil || !idPattern.MatchString(id) {
		return nil
	}
	fi, err := s.links.Lstat(id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return nil
	}
	return s.links.Remove(id)
}

// drop releases every root pred selects, returning each with pred's reason. A root whose link
// cannot be removed stays recorded, so nothing pins a closure without a record listing it.
func (s *session) drop(pred func(Root) (bool, string)) ([]Released, error) {
	var kept []Root
	var out []Released
	var firstErr error
	for _, r := range s.state.Roots {
		if ok, why := pred(r); ok {
			if err := s.unlink(r.ID); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				kept = append(kept, r)
				continue
			}
			out = append(out, Released{Root: r, Reason: why})
			continue
		}
		kept = append(kept, r)
	}
	s.state.Roots = kept
	return out, firstErr
}

func (s *session) expire() ([]Released, error) {
	now, lease := s.g.now(), s.g.lease()
	return s.drop(func(r Root) (bool, string) {
		return now.After(r.Expires(lease)), ReasonExpired
	})
}

// enforceCap releases the least recently renewed roots until at most cap remain, never
// the one named keep.
func (s *session) enforceCap(keep string) ([]Released, error) {
	over := len(s.state.Roots) - s.g.cap()
	if over <= 0 {
		return nil, nil
	}
	cands := make([]Root, 0, len(s.state.Roots))
	for _, r := range s.state.Roots {
		if r.ID != keep {
			cands = append(cands, r)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Renewed.Before(cands[j].Renewed) })
	evict := map[string]bool{}
	for i := 0; i < over && i < len(cands); i++ {
		evict[cands[i].ID] = true
	}
	return s.drop(func(r Root) (bool, string) { return evict[r.ID], ReasonCap })
}

// isStorePath reports whether p is a direct child of the store directory.
func (g *Registry) isStorePath(p string) bool {
	return filepath.IsAbs(p) && filepath.Dir(filepath.Clean(p)) == filepath.Clean(g.storeDir())
}

// Admission is the outcome of Admit.
type Admission struct {
	Root Root
	// New is true when the root did not exist before; Retargeted when it pointed elsewhere.
	New, Retargeted bool
	// Released is every root this admission released: expired leases and the cap.
	Released []Released
}

// Admit makes or renews the managed root for source, a link (as this process spells it,
// and as sourceHost on the host) pointing at target. Only a jail admits: Register must be
// set, because the managed link must be registered under the host's spelling.
//
// The order is: expire the lapsed, make the link, register it, record it, then enforce the
// cap — so a registration the daemon refuses leaves no link and costs no other root its place.
func (g *Registry) Admit(source, sourceHost, target, by string) (Admission, error) {
	if g.Register == nil {
		return Admission{}, errors.New("this process cannot register a root with the host's nix daemon")
	}
	if !g.isStorePath(target) {
		return Admission{}, fmt.Errorf("%s does not point into the store %s", source, g.storeDir())
	}
	s, err := g.open(true)
	if err != nil {
		return Admission{}, err
	}
	defer s.close()
	var adm Admission
	released, err := s.expire()
	adm.Released = append(adm.Released, released...)
	if err != nil {
		return adm, err
	}
	id := RootID(source)
	now := g.now()
	idx := -1
	for i, r := range s.state.Roots {
		if r.ID == id {
			idx = i
		}
	}
	if err := s.relink(id, target); err != nil {
		return adm, err
	}
	linkHost, err := g.Register(g.LinkPath(id))
	if err != nil {
		if idx < 0 {
			_ = s.unlink(id)
		} else if old := s.state.Roots[idx].Target; old != target {
			_ = s.relink(id, old)
		}
		if serr := s.save(); serr != nil {
			return adm, serr
		}
		return adm, err
	}
	if idx < 0 {
		adm.New = true
		s.state.Roots = append(s.state.Roots, Root{ID: id, Source: source, SourceHost: sourceHost,
			Target: target, LinkHost: linkHost, Admitted: now, Renewed: now, By: by})
		idx = len(s.state.Roots) - 1
	} else {
		r := &s.state.Roots[idx]
		adm.Retargeted = r.Target != target
		r.Source, r.SourceHost, r.Target, r.LinkHost, r.Renewed = source, sourceHost, target, linkHost, now
		if by == ByKeep {
			r.By = ByKeep
		}
	}
	adm.Root = s.state.Roots[idx]
	released, err = s.enforceCap(id)
	adm.Released = append(adm.Released, released...)
	if serr := s.save(); serr != nil {
		return adm, serr
	}
	return adm, err
}

// List returns the workspace's managed roots, sorted by source. A workspace with no
// registry has none.
func (g *Registry) List() ([]Root, error) {
	s, err := g.open(false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer s.close()
	out := append([]Root(nil), s.state.Roots...)
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out, nil
}

// Release releases the roots whose id or source (either spelling) is in which; all
// releases every root. It returns what it released and the names it matched nothing for.
func (g *Registry) Release(which []string, all bool) ([]Released, []string, error) {
	s, err := g.open(false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, which, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer s.close()
	want := map[string]bool{}
	for _, w := range which {
		want[w] = true
		if abs, err := filepath.Abs(w); err == nil {
			want[abs] = true
		}
	}
	matched := map[string]bool{}
	out, err := s.drop(func(r Root) (bool, string) {
		hit := all
		for _, k := range []string{r.ID, r.Source, r.SourceHost} {
			if k != "" && want[k] {
				hit = true
				matched[k] = true
			}
		}
		return hit, ReasonAsked
	})
	var missing []string
	for _, w := range which {
		abs, _ := filepath.Abs(w)
		if !matched[w] && !matched[abs] {
			missing = append(missing, w)
		}
	}
	if serr := s.save(); serr != nil && err == nil {
		err = serr
	}
	return out, missing, err
}

// Prune applies the lifecycle now: lapsed leases, links that are gone or no longer point
// into the store, the cap, and managed links no record names (left by a crash between the
// link and the record, or by a registry moved aside). A source pointing at a DIFFERENT store
// path is followed: its managed link is repointed, as the user's own link was.
func (g *Registry) Prune() ([]Released, error) {
	s, err := g.open(false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer s.close()
	out, err := s.expire()
	if err != nil {
		return out, err
	}
	// One pass decides each root's fate from its source link; the drop below applies it.
	var retargetErr error
	why := map[string]string{}
	for i := range s.state.Roots {
		r := &s.state.Roots[i]
		src := r.Source
		if g.SourceSide == SideHost {
			src = r.SourceHost
		}
		if !filepath.IsAbs(src) {
			why[r.ID] = ReasonGone
			continue
		}
		fi, err := os.Lstat(src)
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			why[r.ID] = ReasonGone
			continue
		}
		t, err := os.Readlink(src)
		if err != nil || !g.isStorePath(t) {
			why[r.ID] = ReasonNotStore
			continue
		}
		if t != r.Target {
			if err := s.relink(r.ID, t); err != nil {
				if retargetErr == nil {
					retargetErr = err
				}
			} else {
				r.Target = t
			}
		}
	}
	gone, err := s.drop(func(r Root) (bool, string) {
		w, ok := why[r.ID]
		return ok, w
	})
	out = append(out, gone...)
	if err == nil {
		err = retargetErr
	}
	capped, cerr := s.enforceCap("")
	out = append(out, capped...)
	if err == nil {
		err = cerr
	}
	if s.links != nil {
		named := map[string]bool{}
		for _, r := range s.state.Roots {
			named[r.ID] = true
		}
		if ents, rerr := fs.ReadDir(s.links.FS(), "."); rerr == nil {
			for _, e := range ents {
				if idPattern.MatchString(e.Name()) && !named[e.Name()] {
					if uerr := s.unlink(e.Name()); uerr == nil {
						out = append(out, Released{Root: Root{ID: e.Name()}, Reason: "no record names it"})
					}
				}
			}
		}
	}
	if serr := s.save(); serr != nil && err == nil {
		err = serr
	}
	return out, err
}

package packsrc

// localcopy.go is the LAUNCH-TIME COPY DECISION for a git pack that names a clone with `local`
// (docs/design/local-pack-clones.md §6.2-§6.3, LPC-D8..LPC-D11, slice 5): which copy of the branch a
// launch runs, the clone's working tree or origin's tip, and the one line that says so.
//
//   - THE DECISION ONLY. It stages nothing. Where it says CopyWorkingTree, the working-tree staging
//     (§6.4, slice 6) reads the decision's record (SaveLocalCopyRecord, LoadLocalCopyRecord) and
//     `stagePackEntry` is its call site; until then the pack is staged from origin's tip as before, and the
//     lockfile still records that commit.
//   - ORIGIN'S TIP is the mirror's commit for the ref, from the address the pack names (LPC-D11), after the
//     launch's probe and fetch. The clone's remote only has to name the same repository (chooseCloneFrom,
//     PF-D101/PF-D104 over a one-element list).
//   - NEVER A REFUSAL (LPC-D8). An absent clone, one that is not a repository, one whose remote names
//     another repository, one on another branch, a tag, an unreadable one: each runs origin's copy and says why.
//   - READ ONLY. Every read of the clone is `git -C <clone>` with GIT_OPTIONAL_LOCKS=0 and fsmonitor off, in a
//     budget of its own (LocalCopyDeadline) so a check that spent its deadline waiting on the network still
//     reads the clone.
//   - THE PURE HALF is decideLocalCopy over localFacts; the reading half is Store.localCopyFacts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// LocalCopyDeadline bounds the clone's local git reads for one pack: cheap reads that a huge or wedged
// working tree could still stall, in which case the clone is not used and origin's copy runs.
const LocalCopyDeadline = 5 * time.Second

// LocalCopy names the copy of a `local` pack a launch runs.
type LocalCopy string

const (
	// CopyOrigin is the pack's own mirror at origin's tip.
	CopyOrigin LocalCopy = "origin"
	// CopyWorkingTree is the clone's working tree as it is on disk.
	CopyWorkingTree LocalCopy = "working-tree"
)

// Relations a decision records. "unusable" is a clone that cannot answer (the line says why), "absent" no clone
// at the path, "unverified" a check that could not reach origin, "origin-gone" a fetch that showed origin no
// longer has the branch; the rest are the clone's branch against origin's tip.
const (
	relEqual      = "equal"
	relAhead      = "ahead"
	relBehind     = "behind"
	relDiverged   = "diverged"
	relAbsent     = "absent"
	relUnusable   = "unusable"
	relUnverified = "unverified"
	relOriginGone = "origin-gone"
)

// LocalCopyDecision is one launch's answer for one `local` pack. It is also the pack's launch record
// (LoadLocalCopyRecord).
type LocalCopyDecision struct {
	Name, Ref string
	// Clone is the path `local` names, as the line prints it.
	Clone string
	// Copy is what runs.
	Copy LocalCopy
	// OriginCommit is origin's tip as the mirror holds it ("" when origin has no such branch); CloneCommit is the
	// clone's branch tip ("" when the clone did not answer).
	OriginCommit, CloneCommit string
	Relation                  string
	// Ahead is the clone's commits origin lacks, Behind origin's the clone lacks.
	Ahead, Behind int
	// Dirty is any tracked change or untracked-not-ignored file; Changed counts the first, Untracked the second.
	Dirty              bool
	Changed, Untracked int
	// Verified is whether this launch's check reached origin.
	Verified bool
	// Warn is whether the line is printed as a warning.
	Warn bool
	// Line is the provenance line, without the pack's name.
	Line string
	// At is when the decision was taken, unix seconds.
	At int64
}

// localFacts is everything the decision reads, gathered with git (localCopyFacts) or written by a test.
type localFacts struct {
	Name, Ref, Repo, Path string
	// Tag is a ref that is a tag upstream: no clone answers for it.
	Tag bool
	// Absent is no directory at Path; NotUsed is why a clone that is there cannot answer, "" for one that can.
	Absent  bool
	NotUsed string
	// Remote is the clone's remote that names the repository.
	Remote string
	// OriginCommit and CloneCommit are the two tips; Ahead and Behind the clone's relation to origin's.
	OriginCommit, CloneCommit string
	Ahead, Behind             int
	Changed, Untracked        int
	// Verified is a check that reached origin; FetchCause says why not, LastFetched when it last did.
	Verified    bool
	FetchCause  string
	LastFetched time.Time
	Now         time.Time
}

// decideLocalCopy is the §6.3 rule, pure. It never errors: every input has an answer.
func decideLocalCopy(f localFacts) LocalCopyDecision {
	d := LocalCopyDecision{
		Name: f.Name, Ref: f.Ref, Clone: f.Path, OriginCommit: f.OriginCommit, CloneCommit: f.CloneCommit,
		Ahead: f.Ahead, Behind: f.Behind, Changed: f.Changed, Untracked: f.Untracked,
		Dirty: f.Changed+f.Untracked > 0, Verified: f.Verified, At: f.Now.Unix(),
	}
	remote := f.Remote
	if remote == "" {
		remote = "origin"
	}
	git := func(args ...string) string {
		return shquote.JoinDisplay(append([]string{"git", "-C", f.Path}, args...))
	}
	origin := func(rel, why string, warn bool) LocalCopyDecision {
		d.Copy, d.Relation, d.Warn = CopyOrigin, rel, warn
		d.Line = "origin " + f.Ref + " " + shortCommit(f.OriginCommit) + " (" + why + ")"
		return d
	}
	working := func(rel string, warn bool, line string) LocalCopyDecision {
		d.Copy, d.Relation, d.Warn, d.Line = CopyWorkingTree, rel, warn, line
		return d
	}
	uncommitted := ""
	if n := f.Changed + f.Untracked; n > 0 {
		uncommitted = fmt.Sprintf("%d %s uncommitted", n, plural(n, "file", "files"))
		if f.Untracked > 0 {
			uncommitted += fmt.Sprintf(" (%d untracked)", f.Untracked)
		}
	}
	describe := func(parts ...string) string {
		out := []string{"working tree"}
		for _, p := range append(parts, uncommitted) {
			if p != "" {
				out = append(out, p)
			}
		}
		return "local " + f.Path + " (" + strings.Join(out, ", ") + ")"
	}
	push := func(args ...string) string {
		return git(append([]string{"push"}, append(args, "--", remote, f.Ref)...)...)
	}

	switch {
	case f.Tag:
		return origin(relUnusable, "clone "+f.Path+" not used: "+f.Ref+" is a tag upstream — name the branch the pack follows", true)
	case f.Absent:
		return origin(relAbsent, "no local clone at "+f.Path+" — git clone "+f.Repo+" "+f.Path, false)
	case f.NotUsed != "":
		return origin(relUnusable, "clone "+f.Path+" not used: "+f.NotUsed, true)
	case f.OriginCommit == "":
		// Origin has no such branch (a fetch that succeeded and shows it gone), or the held copy is missing and
		// the check could not say: the clone's work is all there is.
		if !f.Verified {
			return working(relUnverified, true, unverifiedLine(f, describe))
		}
		return working(relOriginGone, true, describe("not on origin — "+push()+" restores it"))
	case !f.Verified:
		return working(relUnverified, true, unverifiedLine(f, describe))
	case f.CloneCommit == f.OriginCommit:
		return working(relEqual, false, describe("equal to origin"))
	case f.Ahead > 0 && f.Behind > 0:
		return working(relDiverged, true, fmt.Sprintf("local %s (working tree) has diverged from origin: %d unpushed, %d behind — %s (if you rebased)",
			f.Path, f.Ahead, f.Behind, push("--force-with-lease")))
	case f.Ahead > 0:
		return working(relAhead, false, describe(fmt.Sprintf("%d unpushed %s", f.Ahead, plural(f.Ahead, "commit", "commits"))))
	case d.Dirty:
		return working(relBehind, true, fmt.Sprintf("local %s (working tree) is behind origin by %d and has uncommitted changes — %s",
			f.Path, f.Behind, git("pull")))
	}
	return origin(relBehind, fmt.Sprintf("clone is %d behind origin and clean — %s", f.Behind, git("pull")), false)
}

// unverifiedLine is the line of a clone run because origin could not be checked.
func unverifiedLine(f localFacts, describe func(...string) string) string {
	last := "never fetched"
	if !f.LastFetched.IsZero() {
		last = "last fetched " + agoWords(f.Now.Sub(f.LastFetched))
	}
	cause := f.FetchCause
	if cause == "" {
		cause = "origin could not be checked"
	}
	return describe("not verified: "+cause+", "+last) + " — yolo pack update when online"
}

// LocalLine is the pack's provenance line for the launch, and whether it prints as a warning; "" for a pack
// with no `local`.
func (o Outcome) LocalLine() (string, bool) {
	if o.Local == nil || o.Local.Line == "" {
		return "", false
	}
	return o.Name + "  " + o.Local.Line, o.Local.Warn
}

// localCopyFor is the launch's decision for one `local` item: it reads the clone and decides. originCommit is
// the mirror's commit for the ref ("" when origin lacks the branch), and fetchErr why the check
// could not reach origin (nil when it did).
func (s *Store) localCopyFor(it refreshItem, originCommit string, fetchErr error, now time.Time) *LocalCopyDecision {
	ctx, cancel := context.WithTimeout(s.parentCtx(), LocalCopyDeadline)
	defer cancel()
	b := budget{ctx: ctx, d: LocalCopyDeadline}
	f := localFacts{
		Name: it.name, Ref: strings.TrimPrefix(it.addr.Ref, "refs/heads/"), Repo: it.addr.Repo, Path: it.local,
		OriginCommit: originCommit, Verified: fetchErr == nil, Now: now,
	}
	if fetchErr != nil {
		f.FetchCause = checkCause(fetchErr)
	}
	if secs := s.stampAt(it.addr); secs > 0 {
		f.LastFetched = time.Unix(secs, 0)
	}
	// A tag or a full commit is no branch, whatever the pack wrote: config cannot know a short name is a tag
	// upstream, the mirror can.
	if kind, _, _, err := s.classifyRef(s.mirrorPath(it.addr.Repo), it.addr.Ref); err == nil && (kind == refTag || kind == refCommit) {
		f.Tag = true
	} else {
		s.readLocalClone(b, it.addr, &f)
	}
	d := decideLocalCopy(f)
	return &d
}

// readLocalClone fills f with what the clone says, or why it cannot answer: no directory, no repository, no
// remote naming the pack's repository, no tracking ref, HEAD off the branch, a read git could not finish.
func (s *Store) readLocalClone(b budget, a Addr, f *localFacts) {
	fi, err := os.Stat(f.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		f.Absent = true
		return
	case err != nil:
		f.NotUsed = "it cannot be read (" + oneLine(err) + ") — fix its permissions, or remove local"
		return
	case !fi.IsDir():
		f.NotUsed = "it is not a directory — git clone " + a.Repo + " <path>, or remove local"
		return
	}
	// -C, never --git-dir: this is a work tree, which the store's own runs (bare mirrors) do not read.
	read := func(args ...string) (string, error) {
		return s.runEnv(b, "", []string{"GIT_OPTIONAL_LOCKS=0"}, append([]string{"-C", f.Path, "-c", "core.fsmonitor=false"}, args...)...)
	}
	top, err := read("rev-parse", "--show-toplevel")
	if top = strings.TrimSpace(top); err != nil || top == "" {
		f.NotUsed = "it is not a git clone — git clone " + a.Repo + " " + f.Path + ", or remove local"
		return
	}
	clone := LocalClone{Dir: filepath.Clean(top)}
	if out, err := read("config", "--get-regexp", `^remote\..*\.url$`); err == nil {
		for _, line := range strings.Split(out, "\n") {
			key, url, ok := strings.Cut(strings.TrimSpace(line), " ")
			if ok {
				name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
				clone.Remotes = append(clone.Remotes, CloneRemote{Name: name, URL: strings.TrimSpace(url)})
			}
		}
	}
	ch := s.chooseCloneFrom(Addr{Kind: KindGit, Repo: a.Repo, Ref: f.Ref}, []LocalClone{clone})
	switch {
	case ch.match == nil && ch.why != "":
		f.NotUsed = ch.why
		return
	case ch.match == nil:
		f.NotUsed = "no remote of the clone names " + a.Repo + " — fix the remote, or the pack's git url"
		return
	}
	f.Remote = ch.match.Remote.Name
	head, err := read("symbolic-ref", "-q", "HEAD")
	if head = strings.TrimSpace(head); head != "refs/heads/"+f.Ref {
		on := "no branch (a detached HEAD)"
		if err == nil {
			on = "branch " + strings.TrimPrefix(head, "refs/heads/")
		}
		f.NotUsed = "it is on " + on + ", not " + f.Ref + " — " +
			shquote.JoinDisplay([]string{"git", "-C", f.Path, "switch", f.Ref})
		return
	}
	tip, err := read("rev-parse", "--verify", "--quiet", "refs/heads/"+f.Ref+"^{commit}")
	if f.CloneCommit = strings.TrimSpace(tip); err != nil || f.CloneCommit == "" {
		f.NotUsed = "its branch " + f.Ref + " has no commit yet"
		return
	}
	if f.OriginCommit != "" && f.OriginCommit != f.CloneCommit {
		common, err := read("rev-parse", "--path-format=absolute", "--git-common-dir")
		if err == nil {
			f.Ahead, f.Behind, err = s.aheadBehind(b, strings.TrimSpace(common), a.Repo, f.CloneCommit, f.OriginCommit)
		}
		if err != nil {
			f.NotUsed = "it could not be compared with origin (" + oneLine(err) + ")"
			return
		}
	}
	status, err := read("status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		f.NotUsed = "its status could not be read (" + oneLine(err) + ")"
		return
	}
	f.Changed, f.Untracked = countStatus(status)
}

// countStatus counts a `status --porcelain=v1 -z` answer's tracked changes and untracked files. A rename or
// copy entry carries its source path as one more field, which is not an entry.
func countStatus(out string) (changed, untracked int) {
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		e := fields[i]
		if len(e) < 3 {
			continue
		}
		switch {
		case e[:2] == "??":
			untracked++
		case e[:2] == "!!":
		default:
			changed++
			if e[0] == 'R' || e[0] == 'C' || e[1] == 'R' || e[1] == 'C' {
				i++
			}
		}
	}
	return changed, untracked
}

// localCopyRecordPath is the pack's launch record, beside the check records. The name is a handle, so it is
// slugged like a mirror's.
func (s *Store) localCopyRecordPath(name string) string {
	return filepath.Join(s.Dir, "checks", "local-"+mirrorSlug(name)+".json")
}

// SaveLocalCopyRecord writes d as its pack's launch record (LPC-D14), atomically: a reader sees the whole
// previous decision or the whole new one. The launch check is its one writer.
func (s *Store) SaveLocalCopyRecord(d LocalCopyDecision) error {
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	path := s.localCopyRecordPath(d.Name)
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".local-copy-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// LoadLocalCopyRecord reads the pack's launch record; nil with no error for a pack never decided.
func (s *Store) LoadLocalCopyRecord(name string) (*LocalCopyDecision, error) {
	data, err := os.ReadFile(s.localCopyRecordPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d LocalCopyDecision
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("the launch record of pack %s is unreadable (%w) — the next launch rewrites it", name, err)
	}
	return &d, nil
}

package run

// keeperrescue.go is the keeper's half of DETECTION and the RESCUE
// (docs/design/workspace-state-safety.md §3.4 and §3.5; SD-D5, SD-D13). The identity stamp, its
// comparison, the rescue directory and the messages are internal/stateguard's; what is here is
// what needs the jail: which of the launch's binds hold the agents' homes, and the copy out of the
// running container.
//
// # Why the copy comes from inside the container
//
// The homes are bind-mounted from `<workspace>/.yolo/setups/<setup>/home` (or the legacy
// `.yolo/home`). After `mv /workspace/.yolo /tmp` inside the jail the host paths are gone, but the
// container's mount namespace still holds every bind, and through them the moved bytes. A copy from
// the host's paths would read nothing, and `podman cp` resolves a bind to its host source, which is
// the very path that is gone. So the keeper runs `tar` INSIDE the container with `<runtime> exec`,
// one stream per bind, and extracts each on the host into the rescue directory. Apple Container
// takes the same exec (`container exec`), against the one bind of the whole home it makes.
// macos-user mounts nothing: its home moves with `.yolo`, so there is nothing to rescue, and the
// stop names the `mv` back instead.
//
// # When
//
// At the keeper's stop of the jail, before the stop, and only when the stamp mismatches. The stop
// takes as long as the copy (SD-D13): a slow stop beats a lost session. A container that ended on
// its own is gone with its binds, and nothing can be copied from it.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/stateguard"
)

// rescueItem is one bind the rescue copies: Dest, where the jail sees it; Rel, its source relative
// to the home, which is where the copy restores to; Excludes, the binds nested under Dest that are
// not this home's (a host cache aliased into ~/.local, ~/.cache under an Apple Container home) or
// are copied as items of their own, as `tar` member names relative to path.Dir(Dest).
type rescueItem struct {
	Dest     string
	Source   string
	Rel      string
	Excludes []string
}

// internalHomeSubtrees are the home binds that hold yolo's own regenerated content (the persistence
// map's PathInternal class): the generated-script anchor, rewritten every boot, and yolo's
// single-file logs, lock and bootstrap scripts. A rescue skips them.
func internalHomeSubtrees() []string {
	var out []string
	for _, b := range append(podmanEarlyHomeBinds(), podmanLateHomeBinds()...) {
		if b.Class == jailcontent.PathInternal && b.Subtree != "bash_history" {
			out = append(out, b.Subtree)
		}
	}
	return out
}

// mountArgv is runCmd up to the image ref, the part argvMounts may read: what follows the image is
// the jailed command.
func mountArgv(runCmd []string, imageRef string) []string {
	if imageRef != "" {
		if i := slices.Index(runCmd, imageRef); i >= 0 {
			return runCmd[:i]
		}
	}
	return runCmd
}

// under reports whether p is base or beneath it.
func under(p, base string) bool {
	if p == base {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(base, "/")+"/")
}

// planRescue is the rescue's plan: every writable bind of the launch's argv whose source is home or
// beneath it and whose destination is the jail's home or beneath it, less yolo's internal ones, in
// argv order. It reads the argv the launch actually ran, so a pack's home dir is in the plan without
// anyone listing it.
func planRescue(runCmd []string, imageRef, home string) []rescueItem {
	if home == "" {
		return nil
	}
	home = filepath.Clean(home)
	mounts := argvMounts(mountArgv(runCmd, imageRef))
	internal := internalHomeSubtrees()
	var items []rescueItem
	for _, m := range mounts {
		if !m.Writable || !under(m.Dest, jailHome) {
			continue
		}
		src := filepath.Clean(m.Source)
		rel, err := filepath.Rel(home, src)
		if err != nil || (rel != "." && !filepath.IsLocal(rel)) {
			continue
		}
		if slices.Contains(internal, filepath.ToSlash(rel)) {
			continue
		}
		items = append(items, rescueItem{Dest: path.Clean(m.Dest), Source: src, Rel: filepath.ToSlash(rel)})
	}
	for i := range items {
		parent := path.Dir(items[i].Dest)
		for _, m := range mounts {
			d := path.Clean(m.Dest)
			if d == items[i].Dest || !under(d, items[i].Dest) {
				continue
			}
			rel := strings.TrimPrefix(d, parent+"/")
			if parent == "/" {
				rel = strings.TrimPrefix(d, "/")
			}
			if !slices.Contains(items[i].Excludes, rel) {
				items[i].Excludes = append(items[i].Excludes, rel)
			}
		}
	}
	return items
}

// rescueSources are the host paths a keeper stamps: the home, and each bind source the rescue would
// copy.
func rescueSources(home string, items []rescueItem) []string {
	out := []string{home}
	for _, it := range items {
		out = append(out, it.Source)
	}
	return out
}

// rescueTarArgv is the command that streams one item out of the running jail: `tar` of the bind's
// last component from its parent, never descending into the excluded nested binds. The exclusions
// are anchored and literal, so a name is matched only where it is and as it is spelled.
func rescueTarArgv(rt, cname string, it rescueItem) []string {
	// No terminal is attached, but every exec into a jail turns the detach sequence off all the
	// same, so no byte of the stream can ever be read as one.
	argv := append([]string{rt, "exec"}, yoloruntime.DetachKeysArgs(rt)...)
	argv = append(argv, cname, "tar", "-c", "-f", "-", "-C", path.Dir(it.Dest), "--anchored", "--no-wildcards")
	for _, x := range it.Excludes {
		argv = append(argv, "--exclude="+x)
	}
	return append(argv, "--", path.Base(it.Dest))
}

// rescueStream starts argv and returns its stdout, and a wait that reports how it ended. A seam, so
// a test streams a tar it made instead of a container's.
var rescueStream = func(argv []string) (io.Reader, func() error, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, left: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return out, func() error {
		err := cmd.Wait()
		var exit *exec.ExitError
		// GNU tar exits 1 when a file changed while it was read: the copy is whole but for that
		// file's latest bytes, which is what a running agent's files are.
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil
		}
		if err != nil && stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return err
	}, nil
}

// limitedWriter keeps the first left bytes written to it and drops the rest.
type limitedWriter struct {
	w    io.Writer
	left int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.left > 0 {
		n := min(len(p), l.left)
		_, _ = l.w.Write(p[:n])
		l.left -= n
	}
	return len(p), nil
}

// rescueNow is the clock a rescue's directory is named by.
var rescueNow = time.Now

// runRescue copies each item out of the running jail into a fresh rescue directory of workspace, and
// records what it copied. It returns the rescue directory and the record, or the error that kept
// any rescue directory from being made.
func runRescue(workspace, home, rt, cname string, items []rescueItem, logf func(string, ...any)) (string, stateguard.RescueRecord, error) {
	rec := stateguard.RescueRecord{Created: rescueNow().UTC()}
	if rel, ok := paths.HomeStateRel(workspace, home); ok {
		rec.HomeRel = filepath.ToSlash(rel)
	}
	r, err := stateguard.NewRescue(workspace, rescueNow())
	if err != nil {
		return "", rec, err
	}
	defer r.Close()
	for _, it := range items {
		fail := func(err error) {
			if rec.Failed == nil {
				rec.Failed = map[string]string{}
			}
			rec.Failed[it.Rel] = err.Error()
			logf("keeper: the rescue could not copy %s: %v", it.Dest, err)
		}
		stream, wait, err := rescueStream(rescueTarArgv(rt, cname, it))
		if err != nil {
			fail(err)
			continue
		}
		n, xerr := r.Extract(stream, path.Base(it.Dest), it.Rel)
		// Drain what the extraction left, so the stream's writer ends rather than blocks.
		_, _ = io.Copy(io.Discard, stream)
		werr := wait()
		switch {
		case xerr != nil:
			fail(xerr)
		case werr != nil:
			fail(werr)
		default:
			rec.Copied = append(rec.Copied, it.Rel)
			logf("keeper: the rescue copied %s (%d bytes)", it.Dest, n)
		}
	}
	if err := r.WriteRecord(rec); err != nil {
		logf("keeper: the rescue could not write its record: %v", err)
	}
	return r.Dir, rec, nil
}

// stampStateIdentity takes the jail's identity stamp, for its start record: before the container
// starts, so it names the directories the binds are made from.
func (k *keeper) stampStateIdentity() *stateguard.Stamp {
	p := k.plan
	home := p.Home
	if !paths.IsWorkspaceHomeState(p.Workspace, home) {
		home = ""
	}
	sources := []string{}
	if home != "" {
		sources = rescueSources(home, planRescue(p.RunCmd, p.ImageRef, home))
	}
	return stateguard.Take(p.Workspace, home, sources)
}

// rescueIfMoved is the keeper's check at its stop of a running container: on a mismatch that lost
// the host's path to a home it copies the homes out first, then prints where they went and the
// command that restores them. A mismatch of the durable directory alone is named, since the rescue
// cannot reach it.
func (k *keeper) rescueIfMoved() {
	p := k.plan
	k.recMu.Lock()
	stamp := k.record.StateIdentity
	k.recMu.Unlock()
	f := stateguard.Check(stamp)
	if !f.Moved() {
		return
	}
	if !f.HomesLost() {
		for _, l := range stateguard.Warning(f, p.Workspace, stateguard.Mounted) {
			k.sink.logf("yolo: %s", l)
		}
		return
	}
	items := planRescue(p.RunCmd, p.ImageRef, stamp.Home)
	if len(items) == 0 {
		k.sink.logf("yolo: %s was moved or deleted while the jail ran, and yolo found no home of this jail to copy out before stopping it.", filepath.Join(p.Workspace, ".yolo"))
		return
	}
	k.sink.logf("yolo: %s was moved or deleted while the jail ran; copying the agents' homes out of the jail before it stops. This can take a while.", filepath.Join(p.Workspace, ".yolo"))
	sp := k.o.Perf.Span("keeper.rescue")
	dir, rec, err := runRescue(p.Workspace, stamp.Home, p.Runtime, p.Cname, items, k.sink.logf)
	sp.End()
	if err != nil {
		k.sink.logf("yolo: the agents' homes could not be copied out of the jail (%v), so they are lost as it stops. What happened is recorded in %s.", err, keeperLogPath(k.stateKey()))
		return
	}
	if len(rec.Failed) > 0 {
		k.sink.logf("yolo: some of the agents' homes could not be copied: %s. The rest are in %s.", strings.Join(slices.Sorted(maps.Keys(rec.Failed)), ", "), dir)
	}
	k.sink.logf("yolo: %s", stateguard.StopLine(p.Workspace, dir, stamp.Home))
}

// noteMovedAtNotch is the same check at a macos-user keeper's end: nothing is mounted, so nothing
// is copied, and the line names the `mv` back.
func (k *keeper) noteMovedAtNotch() {
	k.recMu.Lock()
	stamp := k.record.StateIdentity
	k.recMu.Unlock()
	for _, l := range stateguard.Warning(stateguard.Check(stamp), k.plan.Workspace, stateguard.Moved) {
		k.sink.logf("yolo: %s", l)
	}
}

// stateMovedLines is what a keeper record's identity stamp says now: the warning lines for a
// mismatch, none when nothing moved or the record has no stamp.
func stateMovedLines(rec keeperRecord) []string {
	if rec.StateIdentity == nil {
		return nil
	}
	setup := stateguard.Mounted
	if rec.Notch != "" {
		setup = stateguard.Moved
	}
	return stateguard.Warning(stateguard.Check(rec.StateIdentity), rec.StateIdentity.Workspace, setup)
}

// warnStateMoved is an arrival's check (SD-D5): a session entering a jail whose `.yolo` moved under
// it is told so on its own terminal, before its shell starts, with the step that keeps the homes. It
// never refuses: the session goes ahead.
func (o *Options) warnStateMoved(rec keeperRecord) {
	lines := stateMovedLines(rec)
	if len(lines) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	out.print("[bold yellow]Warning:[/bold yellow] " + richtext.Escape(lines[0]))
	for _, l := range lines[1:] {
		out.print(richtext.Escape(l))
	}
}

// StateMovedFindings is `yolo check`'s view of the same check: for each running jail of workspace
// whose keeper recorded an identity stamp, the warning lines when its `.yolo` is not the one it
// started on. A jail whose keeper is gone is not asked: its binds are gone with it.
//
// EVERY JAIL OF THE WORKSPACE, by its keeper's record (workspaceKeeperKeys): the legacy name an
// older yolo keyed it by and every setup-qualified name (named-setups-plan.md SUP-D2). The keys
// come from the records themselves, not from .yolo/setups, because the `.yolo` this check is
// about may be the one that moved.
func StateMovedFindings(workspace string) []string {
	var out []string
	for _, key := range workspaceKeeperKeys(workspace) {
		rec, ok := readKeeperRecord(key)
		if !ok || probeKeeper(key) == keeperGone {
			continue
		}
		out = append(out, stateMovedLines(rec)...)
	}
	return out
}

// workspaceKeeperKeys lists, sorted, every keeper key with a start record whose jail is one of
// workspace's (runtime.IsJailOf of the key's jail name): the container key and its macos-user keys
// (macosUserKeeperNotches), under the legacy name or any setup's.
func workspaceKeeperKeys(workspace string) []string {
	entries, err := os.ReadDir(ownerPIDDir())
	if err != nil {
		return nil
	}
	notches := map[string]bool{}
	for _, n := range macosUserKeeperNotches {
		notches[n] = true
	}
	var out []string
	for _, e := range entries {
		key, ok := strings.CutSuffix(e.Name(), ".keeper.json")
		if !ok {
			continue
		}
		cname := key
		if i := strings.LastIndexByte(key, '.'); i >= 0 && notches[key[i+1:]] {
			cname = key[:i]
		}
		if yoloruntime.IsJailOf(workspace, cname) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// keyedNames are the keys a workspace's macos-user keepers hold (macosUserKeeperNotches).
func keyedNames(cname string) []string {
	var out []string
	for _, n := range macosUserKeeperNotches {
		out = append(out, keeperKey(cname, n))
	}
	return out
}

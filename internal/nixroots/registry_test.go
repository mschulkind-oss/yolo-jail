package nixroots

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixroots/nixrootstest"
)

// regFixture is a workspace with a registry whose managed links register with a fake daemon
// under a fake host spelling, a store holding n paths, and a clock the test moves.
type regFixture struct {
	ws, store string
	paths     []string
	daemon    *nixrootstest.Daemon
	now       time.Time
	reg       *Registry
}

func newRegFixture(t *testing.T, n int) *regFixture {
	t.Helper()
	f := &regFixture{ws: resolvedTempDir(t), store: resolvedTempDir(t), now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	for i := 0; i < n; i++ {
		p := filepath.Join(f.store, string(rune('a'+i))+"23456789abcdfghijklmnpqrsvwxyz-p")
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		f.paths = append(f.paths, p)
	}
	f.daemon = nixrootstest.Start(t, nixrootstest.Options{})
	registrar := Registrar{Map: HostMap{f.ws: "/host/proj"}, Socket: f.daemon.Socket, StoreDir: f.store}
	f.reg = &Registry{Workspace: f.ws, StoreDir: f.store, Now: func() time.Time { return f.now },
		Register: registrar.Register, Lease: 7 * 24 * time.Hour, Cap: 3}
	return f
}

// userLink makes a user's link in the workspace, pointing at target.
func (f *regFixture) userLink(t *testing.T, name, target string) string {
	t.Helper()
	p := filepath.Join(f.ws, name)
	_ = os.Remove(p)
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *regFixture) admit(t *testing.T, name string, target string) Admission {
	t.Helper()
	src := f.userLink(t, name, target)
	adm, err := f.reg.Admit(src, "/host/proj/"+name, target, ByWatch)
	if err != nil {
		t.Fatal(err)
	}
	return adm
}

func TestAdmitMakesAManagedLinkAndRegistersItsHostSpelling(t *testing.T) {
	f := newRegFixture(t, 1)
	adm := f.admit(t, "result", f.paths[0])
	if !adm.New {
		t.Error("first admission not reported as new")
	}
	id := RootID(filepath.Join(f.ws, "result"))
	link := filepath.Join(f.ws, ".yolo", "nix-roots", "links", id)
	if got, err := os.Readlink(link); err != nil || got != f.paths[0] {
		t.Fatalf("managed link = %q, %v; want -> %s", got, err, f.paths[0])
	}
	want := "/host/proj/.yolo/nix-roots/links/" + id
	if !slices.Contains(f.daemon.Roots(), want) {
		t.Errorf("daemon got %v, want %s", f.daemon.Roots(), want)
	}
	if adm.Root.LinkHost != want {
		t.Errorf("recorded link_host %q, want %q", adm.Root.LinkHost, want)
	}
	// The user's own link is untouched.
	if got, _ := os.Readlink(filepath.Join(f.ws, "result")); got != f.paths[0] {
		t.Errorf("user link changed to %q", got)
	}
	roots, err := f.reg.List()
	if err != nil || len(roots) != 1 || roots[0].Source != filepath.Join(f.ws, "result") {
		t.Fatalf("List = %+v, %v", roots, err)
	}
}

func TestAdmitRenewsAndFollowsARetarget(t *testing.T) {
	f := newRegFixture(t, 2)
	f.admit(t, "result", f.paths[0])
	f.now = f.now.Add(time.Hour)
	adm := f.admit(t, "result", f.paths[1])
	if adm.New || !adm.Retargeted {
		t.Errorf("second admission New=%v Retargeted=%v; want a retarget", adm.New, adm.Retargeted)
	}
	if !adm.Root.Renewed.Equal(f.now) {
		t.Errorf("renewed %v, want %v", adm.Root.Renewed, f.now)
	}
	if got, _ := os.Readlink(f.reg.LinkPath(adm.Root.ID)); got != f.paths[1] {
		t.Errorf("managed link -> %q, want %q", got, f.paths[1])
	}
	roots, _ := f.reg.List()
	if len(roots) != 1 {
		t.Errorf("one source made %d roots", len(roots))
	}
}

func TestAdmitReleasesALapsedLease(t *testing.T) {
	f := newRegFixture(t, 2)
	old := f.admit(t, "old", f.paths[0])
	f.now = f.now.Add(8 * 24 * time.Hour)
	adm := f.admit(t, "new", f.paths[1])
	if len(adm.Released) != 1 || adm.Released[0].Root.ID != old.Root.ID || adm.Released[0].Reason != ReasonExpired {
		t.Fatalf("released %+v, want the old root as expired", adm.Released)
	}
	if _, err := os.Lstat(f.reg.LinkPath(old.Root.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expired managed link still there: %v", err)
	}
}

func TestAdmitPastTheCapReleasesTheLeastRecentlyRenewed(t *testing.T) {
	f := newRegFixture(t, 4)
	var ids []string
	for i, name := range []string{"a", "b", "c"} {
		ids = append(ids, f.admit(t, name, f.paths[i]).Root.ID)
		f.now = f.now.Add(time.Minute)
	}
	// Renew "a", so "b" is now the least recently renewed.
	f.admit(t, "a", f.paths[0])
	f.now = f.now.Add(time.Minute)
	adm := f.admit(t, "d", f.paths[3])
	if len(adm.Released) != 1 || adm.Released[0].Root.ID != ids[1] || adm.Released[0].Reason != ReasonCap {
		t.Fatalf("released %+v, want b over the cap", adm.Released)
	}
	roots, _ := f.reg.List()
	if len(roots) != 3 {
		t.Errorf("%d roots after the cap, want 3", len(roots))
	}
}

func TestAdmitThatTheDaemonRefusesLeavesNoLinkAndNoRecord(t *testing.T) {
	f := newRegFixture(t, 1)
	refusing := nixrootstest.Start(t, nixrootstest.Options{Reject: "no"})
	f.reg.Register = Registrar{Map: HostMap{f.ws: "/host/proj"}, Socket: refusing.Socket, StoreDir: f.store}.Register
	src := f.userLink(t, "result", f.paths[0])
	_, err := f.reg.Admit(src, "/host/proj/result", f.paths[0], ByKeep)
	var rej *RejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if _, err := os.Lstat(f.reg.LinkPath(RootID(src))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("refused root left its link: %v", err)
	}
	if roots, _ := f.reg.List(); len(roots) != 0 {
		t.Errorf("refused root recorded: %+v", roots)
	}
}

func TestAdmitRefusesATargetOutsideTheStore(t *testing.T) {
	f := newRegFixture(t, 0)
	if _, err := f.reg.Admit(filepath.Join(f.ws, "x"), "", "/etc/passwd", ByKeep); err == nil {
		t.Fatal("admitted a target outside the store")
	}
}

func TestReleaseByIDSourceAndAll(t *testing.T) {
	f := newRegFixture(t, 3)
	a := f.admit(t, "a", f.paths[0])
	f.admit(t, "b", f.paths[1])
	f.admit(t, "c", f.paths[2])

	rel, missing, err := f.reg.Release([]string{a.Root.ID, filepath.Join(f.ws, "b"), "nope"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rel) != 2 || !slices.Equal(missing, []string{"nope"}) {
		t.Fatalf("released %d, missing %v", len(rel), missing)
	}
	if _, err := os.Lstat(f.reg.LinkPath(a.Root.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("released link still there")
	}
	// The user's links are never touched.
	if _, err := os.Lstat(filepath.Join(f.ws, "a")); err != nil {
		t.Errorf("release removed the user's link: %v", err)
	}
	rel, _, err = f.reg.Release(nil, true)
	if err != nil || len(rel) != 1 {
		t.Fatalf("release --all released %d, %v", len(rel), err)
	}
	if roots, _ := f.reg.List(); len(roots) != 0 {
		t.Errorf("roots left after --all: %+v", roots)
	}
}

func TestPruneReleasesGoneSourcesFollowsRetargetsAndSweepsStrayLinks(t *testing.T) {
	f := newRegFixture(t, 3)
	gone := f.admit(t, "gone", f.paths[0])
	moved := f.admit(t, "moved", f.paths[1])
	if err := os.Remove(filepath.Join(f.ws, "gone")); err != nil {
		t.Fatal(err)
	}
	f.userLink(t, "moved", f.paths[2])
	stray := f.reg.LinkPath("00000000000000aa")
	if err := os.Symlink(f.paths[0], stray); err != nil {
		t.Fatal(err)
	}

	rel, err := f.reg.Prune()
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, r := range rel {
		reasons = append(reasons, r.Root.ID+":"+r.Reason)
	}
	if !slices.Contains(reasons, gone.Root.ID+":"+ReasonGone) || !slices.Contains(reasons, "00000000000000aa:no record names it") {
		t.Errorf("pruned %v", reasons)
	}
	if got, _ := os.Readlink(f.reg.LinkPath(moved.Root.ID)); got != f.paths[2] {
		t.Errorf("retargeted source's managed link -> %q, want %q", got, f.paths[2])
	}
	roots, _ := f.reg.List()
	if len(roots) != 1 || roots[0].Target != f.paths[2] {
		t.Errorf("after prune: %+v", roots)
	}
}

// The host checks the HOST spelling of a source, which is all it can see.
func TestPruneOnTheHostReadsTheHostSpelling(t *testing.T) {
	f := newRegFixture(t, 1)
	adm := f.admit(t, "result", f.paths[0])
	host := &Registry{Workspace: f.ws, StoreDir: f.store, Now: f.reg.Now, SourceSide: SideHost}
	// The recorded host spelling (/host/proj/result) does not exist here, so to the host
	// the link is gone.
	rel, err := host.Prune()
	if err != nil || len(rel) != 1 || rel[0].Root.ID != adm.Root.ID || rel[0].Reason != ReasonGone {
		t.Fatalf("host prune = %+v, %v", rel, err)
	}
}

// A registry is jail-writable, and the host deletes in it: a links directory the jail
// replaced with a link must be refused, not followed.
func TestALinkedLinksDirectoryIsRefused(t *testing.T) {
	f := newRegFixture(t, 1)
	f.admit(t, "result", f.paths[0])
	elsewhere := resolvedTempDir(t)
	victim := filepath.Join(elsewhere, RootID(filepath.Join(f.ws, "result")))
	if err := os.Symlink("/nix/store/x", victim); err != nil {
		t.Fatal(err)
	}
	links := filepath.Join(f.reg.Dir(), "links")
	if err := os.RemoveAll(links); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, links); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.reg.Release(nil, true); err == nil {
		t.Error("release through a linked links directory succeeded")
	}
	if _, err := os.Lstat(victim); err != nil {
		t.Errorf("a file outside the registry was removed: %v", err)
	}
}

func TestACorruptRegistryIsReportedNotOverwritten(t *testing.T) {
	f := newRegFixture(t, 1)
	f.admit(t, "result", f.paths[0])
	file := filepath.Join(f.reg.Dir(), "roots.json")
	if err := os.WriteFile(file, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := f.reg.List()
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a CorruptError", err)
	}
	if b, _ := os.ReadFile(file); string(b) != "{" {
		t.Error("corrupt registry was overwritten")
	}
}

func TestListOfAWorkspaceWithNoRegistryIsEmpty(t *testing.T) {
	reg := &Registry{Workspace: resolvedTempDir(t)}
	roots, err := reg.List()
	if err != nil || len(roots) != 0 {
		t.Fatalf("List = %+v, %v", roots, err)
	}
	if _, err := os.Lstat(filepath.Join(reg.Workspace, ".yolo")); !errors.Is(err, os.ErrNotExist) {
		t.Error("List created the state directory")
	}
}

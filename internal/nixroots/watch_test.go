package nixroots

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// watchFixture is a registry fixture plus a stand-in for the host's auto dir. An entry is
// what the host daemon writes: a symlink named by a hash, pointing at the path string the
// client sent.
func watchFixture(t *testing.T) (*regFixture, *Watcher) {
	t.Helper()
	f := newRegFixture(t, 3)
	f.reg.Cap = 64
	auto := resolvedTempDir(t)
	return f, &Watcher{AutoDir: auto, Map: HostMap{f.ws: "/host/proj"}, Registry: f.reg}
}

func autoEntry(t *testing.T, w *Watcher, name, sent string) {
	t.Helper()
	tmp := filepath.Join(w.AutoDir, name+".tmp")
	if err := os.Symlink(sent, tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(w.AutoDir, name)); err != nil {
		t.Fatal(err)
	}
}

func TestTheWatcherKeepsAJailLinkTheDaemonWasAskedToRoot(t *testing.T) {
	f, w := watchFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { cancel(); <-done }()

	src := f.userLink(t, "result", f.paths[0])
	// Let the watch be placed before the event, as it is at boot (Run watches, then scans).
	time.Sleep(100 * time.Millisecond)
	autoEntry(t, w, "aaaa", src)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		roots, _ := f.reg.List()
		if len(roots) == 1 && roots[0].Source == src && roots[0].SourceHost == "/host/proj/result" &&
			roots[0].Target == f.paths[0] {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	roots, _ := f.reg.List()
	t.Fatalf("watcher kept %+v, want one root for %s", roots, src)
}

func TestConsiderSkipsWhatIsNotThisJailsLink(t *testing.T) {
	f, w := watchFixture(t)
	// A path that does not exist here (another jail's, or the host's own).
	autoEntry(t, w, "a", "/home/someone/proj/result")
	// A link that is not into the store.
	notStore := filepath.Join(f.ws, "notstore")
	_ = os.Symlink("/etc/hosts", notStore)
	autoEntry(t, w, "b", notStore)
	// A link under no mount the host can see.
	outside := resolvedTempDir(t)
	scratch := filepath.Join(outside, "result")
	_ = os.Symlink(f.paths[0], scratch)
	autoEntry(t, w, "c", scratch)
	for _, n := range []string{"a", "b", "c", "missing"} {
		if w.Consider(n, false) {
			t.Errorf("entry %s was kept", n)
		}
	}
	if roots, _ := f.reg.List(); len(roots) != 0 {
		t.Errorf("kept %+v", roots)
	}
}

// The managed root's own registration lands in the auto dir too; it must not feed back.
func TestConsiderSkipsTheRegistrysOwnLinks(t *testing.T) {
	f, w := watchFixture(t)
	adm := f.admit(t, "result", f.paths[0])
	autoEntry(t, w, "own-jail", f.reg.LinkPath(adm.Root.ID))
	autoEntry(t, w, "own-host", adm.Root.LinkHost)
	if w.Consider("own-jail", false) || w.Consider("own-host", false) {
		t.Error("the watcher admitted its own managed link")
	}
	if roots, _ := f.reg.List(); len(roots) != 1 {
		t.Errorf("roots = %+v", roots)
	}
}

// Seeing an old entry is not a new request: a scan admits a missing root but renews none.
func TestAScanAdmitsButDoesNotRenew(t *testing.T) {
	f, w := watchFixture(t)
	src := f.userLink(t, "result", f.paths[0])
	autoEntry(t, w, "e", src)
	if n := w.Scan(); n != 1 {
		t.Fatalf("first scan kept %d, want 1", n)
	}
	first, _ := f.reg.List()
	f.now = f.now.Add(time.Hour)
	if n := w.Scan(); n != 0 {
		t.Errorf("second scan kept %d, want 0", n)
	}
	again, _ := f.reg.List()
	if !again[0].Renewed.Equal(first[0].Renewed) {
		t.Error("a scan renewed a root")
	}
	// A fresh event does renew.
	if !w.Consider("e", false) {
		t.Fatal("event not admitted")
	}
	renewed, _ := f.reg.List()
	if !renewed[0].Renewed.Equal(f.now) {
		t.Error("an event did not renew")
	}
}

// THE HANDOFF FENCE (NR-D7): the watcher pins the target before it registers the managed
// link, registers it on the pin's connection, and pins again after the registration is
// acknowledged — all on one connection, so the temp root spans the whole handoff.
func TestTheWatcherPinsRegistersAndFencesOnOneConnection(t *testing.T) {
	f, w := watchFixture(t)
	w.Registrar = &Registrar{Map: w.Map, Socket: f.daemon.Socket, StoreDir: f.store}
	src := f.userLink(t, "result", f.paths[0])
	autoEntry(t, w, "e", src)
	if !w.Consider("e", false) {
		t.Fatal("not kept")
	}
	link := "/host/proj/.yolo/nix-roots/links/" + RootID(src)
	ops := f.daemon.Ops()
	if len(ops) != 3 {
		t.Fatalf("ops = %v, want pin, register, fence", ops)
	}
	conn := ops[0][:strings.Index(ops[0], ":")]
	want := []string{conn + ":temp:" + f.paths[0], conn + ":indirect:" + link, conn + ":temp:" + f.paths[0]}
	if !slices.Equal(ops, want) {
		t.Errorf("ops = %v\nwant %v", ops, want)
	}
}

// The CALL SITE of the fence: the watcher WatchMain builds pins through its registrar.
func TestTheProductionWatcherIsFenced(t *testing.T) {
	w := newWatcher("/auto", "/workspace", HostMap{"/workspace": "/h"}, func(string) string { return "" })
	if w.Registrar == nil || w.Registry.Register == nil {
		t.Fatal("the production watcher registers without a pin")
	}
}

// A release sticks across a rescan: the old entry is the request the release answered.
func TestAScanDoesNotReviveAReleasedRoot(t *testing.T) {
	f, w := watchFixture(t)
	src := f.userLink(t, "result", f.paths[0])
	autoEntry(t, w, "e", src)
	old := time.Now().Add(-time.Hour)
	_ = lchtimes(filepath.Join(w.AutoDir, "e"), old)
	if w.Scan() != 1 {
		t.Fatal("first scan did not keep the root")
	}
	f.now = time.Now()
	if _, _, err := f.reg.Release(nil, true); err != nil {
		t.Fatal(err)
	}
	if n := w.Scan(); n != 0 {
		t.Errorf("a rescan revived a released root (%d)", n)
	}
	// A fresh request after the release is honored.
	if !w.Consider("e", false) {
		t.Error("a fresh event after a release was not kept")
	}
}

func TestTheWatcherIgnoresNixsTempNames(t *testing.T) {
	f, w := watchFixture(t)
	src := f.userLink(t, "result", f.paths[0])
	if err := os.Symlink(src, filepath.Join(w.AutoDir, "abc.tmp-123-456")); err != nil {
		t.Fatal(err)
	}
	if w.Consider("abc.tmp-123-456", false) {
		t.Error("kept a temp name")
	}
}

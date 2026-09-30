package modelmenu

// shared_test.go pins the host's menu store (docs/design/model-lists-and-pickers.md MM-D27): a
// menu per cache key that no other launch overwrites, a shared lock each launch holds for its
// program's life, and collection only by a launch that writes a new menu while no program holds
// one. The program is newFixture's shell stub, so no agent runs.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// request is the Request `yolo host --` makes of f's program for list.
func (f *fixture) request(list ...ListEntry) Request {
	return Request{Bin: "app", Spec: spec, List: list, Program: []string{f.program}, Prefix: "yolo host: "}
}

// menus is the menu files dir holds, sorted.
func menus(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// liveLockTaken reports whether some holder keeps dir's live lock, asked the way a second launch
// asks: an exclusive, non-blocking lock on a descriptor of its own.
func liveLockTaken(t *testing.T, dir string) bool {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, LiveLockFile), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return false
	}
	if err != syscall.EWOULDBLOCK {
		t.Fatalf("probing the live lock: %v", err)
	}
	return true
}

// TWO LAUNCHES WITH DIFFERENT LISTS KEEP TWO MENUS: each is named by its own key and neither
// replaces nor removes the other while its program runs, and the flag names the launch's own.
func TestWriteInKeepsEveryMenuARunningProgramHolds(t *testing.T) {
	f := newFixture(t, `{}`, catalog, 0)
	dir := filepath.Join(t.TempDir(), "menus", "pack", "app")
	var errw bytes.Buffer
	a := f.request(ListEntry{ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol"}, ListEntry{ID: "gpt-6-astra"}).WriteIn(dir, &errw)
	if a == nil {
		t.Fatalf("no menu for a list the catalog covers:\n%s", errw.String())
	}
	defer a.Close()
	if want := []string{"-c", "model_catalog_json=" + a.Path}; !reflect.DeepEqual(a.Flag, want) {
		t.Errorf("flag = %q, want %q", a.Flag, want)
	}
	if filepath.Dir(a.Path) != dir || filepath.Base(a.Path) != f.request(ListEntry{ID: "gpt-6.1-sol",
		Name: "GPT-6.1 Sol"}, ListEntry{ID: "gpt-6-astra"}).Key()+".json" {
		t.Errorf("menu path = %s, want <key>.json in %s", a.Path, dir)
	}
	b := f.request(ListEntry{ID: "gpt-6-astra"}).WriteIn(dir, &errw)
	if b == nil {
		t.Fatalf("no menu for the second list:\n%s", errw.String())
	}
	defer b.Close()
	if a.Path == b.Path {
		t.Fatalf("two lists share one menu path %s", a.Path)
	}
	for _, h := range []*Held{a, b} {
		if _, err := os.Stat(h.Path); err != nil {
			t.Errorf("the menu %s a running program holds is gone: %v", h.Path, err)
		}
	}
	data, _ := os.ReadFile(a.Path)
	if models := decodeMenu(t, data); len(models) != 2 {
		t.Errorf("the first launch's menu was overwritten by the second's: %v", models)
	}
	if !liveLockTaken(t, dir) {
		t.Error("no launch holds the directory's live lock while two programs hold menus")
	}
}

// ONCE NO PROGRAM HOLDS ONE, THE NEXT LAUNCH THAT WRITES A MENU REMOVES THE OTHERS, and a launch
// that only reuses its menu removes nothing.
func TestWriteInCollectsMenusOnlyWhenNoProgramHoldsOne(t *testing.T) {
	f := newFixture(t, `{}`, catalog, 0)
	dir := t.TempDir()
	var errw bytes.Buffer
	sol, astra := ListEntry{ID: "gpt-6.1-sol"}, ListEntry{ID: "gpt-6-astra"}
	a := f.request(sol).WriteIn(dir, &errw)
	b := f.request(astra).WriteIn(dir, &errw)
	if a == nil || b == nil {
		t.Fatalf("setup: menus not written:\n%s", errw.String())
	}
	a.Close()
	// b's program still runs: a new menu now removes nothing.
	c := f.request(sol, astra).WriteIn(dir, &errw)
	if c == nil {
		t.Fatalf("no third menu:\n%s", errw.String())
	}
	if got := menus(t, dir); len(got) != 3 {
		t.Errorf("menus while one program runs = %v, want all three kept", got)
	}
	b.Close()
	c.Close()
	// Nothing runs, and a reuse writes nothing new, so it collects nothing.
	again := f.request(sol).WriteIn(dir, &errw)
	if again == nil || again.Path != a.Path {
		t.Fatalf("the unchanged list did not reuse its menu (%v)", again)
	}
	again.Close()
	if got := menus(t, dir); len(got) != 3 {
		t.Errorf("menus after a reuse = %v, want all three: only a new menu collects", got)
	}
	// Nothing runs and this launch writes a new menu: the others, and their key files, go.
	d := f.request(astra, sol).WriteIn(dir, &errw)
	if d == nil {
		t.Fatalf("no fourth menu:\n%s", errw.String())
	}
	defer d.Close()
	if got, want := menus(t, dir), []string{filepath.Base(d.Path)}; !reflect.DeepEqual(got, want) {
		t.Errorf("menus after a new one with none running = %v, want %v alone", got, want)
	}
	for _, e := range []string{decideLockFile, LiveLockFile, filepath.Base(d.Path) + ".key"} {
		if _, err := os.Stat(filepath.Join(dir, e)); err != nil {
			t.Errorf("%s went with the collected menus: %v", e, err)
		}
	}
	if _, err := os.Stat(a.Path + ".key"); !os.IsNotExist(err) {
		t.Errorf("a collected menu's key file stayed (%v)", err)
	}
}

// THE SAME BUILD AS THE JAIL'S: a menu is reused without running the program again, and every
// launch that hands one over names the listed id it leaves out, in the host's words.
func TestWriteInReusesAMenuAndRepeatsTheMissingIDWarning(t *testing.T) {
	f := newFixture(t, `{}`, catalog, 0)
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		var errw bytes.Buffer
		h := f.request(ListEntry{ID: "gpt-6.1-sol"}, ListEntry{ID: "gpt-6-luna"}).WriteIn(dir, &errw)
		if h == nil {
			t.Fatalf("launch %d: no menu:\n%s", i+1, errw.String())
		}
		h.Close()
		if !strings.Contains(errw.String(), "yolo host: app's own model catalog has no gpt-6-luna") {
			t.Errorf("launch %d said %q, want the missing id named in the host's words", i+1, errw.String())
		}
	}
	if n := f.runCount(t); n != 1 {
		t.Errorf("the program ran %d times for two launches of one list, want once", n)
	}
}

// NO LIST, NO MENU, NO LOCK: nothing is run or held, and a catalog that cannot be read is a
// warning, never a refusal.
func TestWriteInNamesNoMenuWhereThereIsNone(t *testing.T) {
	f := newFixture(t, `{}`, catalog, 0)
	dir := t.TempDir()
	var errw bytes.Buffer
	if h := f.request().WriteIn(dir, &errw); h != nil {
		t.Errorf("an empty list held %s", h.Path)
	}
	if f.runCount(t) != 0 || errw.Len() != 0 {
		t.Errorf("an empty list ran the program %d times and said %q", f.runCount(t), errw.String())
	}
	bad := newFixture(t, `{}`, "not json", 3)
	if h := bad.request(ListEntry{ID: "gpt-6.1-sol"}).WriteIn(dir, &errw); h != nil {
		t.Errorf("an unreadable catalog held %s", h.Path)
	}
	if !strings.Contains(errw.String(), "yolo host: could not read app's own model catalog") {
		t.Errorf("stderr = %q, want the catalog failure named", errw.String())
	}
	if liveLockTaken(t, dir) {
		t.Error("a launch with no menu kept the live lock")
	}
}

// THE LOCK OUTLIVES THE EXEC: KeepAcrossExec clears close-on-exec on the descriptor holding it,
// so the program yolo host execs holds the lock itself, and Close gives it back.
func TestHeldKeepsItsLockAcrossTheExec(t *testing.T) {
	f := newFixture(t, `{}`, catalog, 0)
	dir := t.TempDir()
	var errw bytes.Buffer
	h := f.request(ListEntry{ID: "gpt-6.1-sol"}).WriteIn(dir, &errw)
	if h == nil {
		t.Fatalf("no menu:\n%s", errw.String())
	}
	fd := h.lock.Fd()
	if flags, err := unix.FcntlInt(fd, unix.F_GETFD, 0); err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("before KeepAcrossExec the lock is not close-on-exec (flags %d, %v); the resident path "+
			"would leak it into every child", flags, err)
	}
	if err := h.KeepAcrossExec(); err != nil {
		t.Fatal(err)
	}
	if flags, err := unix.FcntlInt(fd, unix.F_GETFD, 0); err != nil || flags&unix.FD_CLOEXEC != 0 {
		t.Errorf("after KeepAcrossExec the lock is still close-on-exec (flags %d, %v): it would end at "+
			"the exec", flags, err)
	}
	if !liveLockTaken(t, dir) {
		t.Error("the menu's lock is not held")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if liveLockTaken(t, dir) {
		t.Error("Close did not give the lock back")
	}
	if err := h.Close(); err != nil {
		t.Errorf("a second Close = %v, want nil", err)
	}
}

// TWO FIRST LAUNCHES AT ONCE READ THE CATALOG ONCE (MM-D28 (6)): the decision lock is held across
// the catalog run, so the second launch waits for the first and reuses the menu it wrote. The
// same lock is what keeps a writer from collecting a menu between another launch's build and its
// shared lock; this pins that the build takes it.
func TestWriteInSerializesTwoFirstLaunchesOnTheDecisionLock(t *testing.T) {
	f := newFixture(t, `{}`, catalog, 0)
	slow := "#!/bin/bash\necho \"$*\" >> '" + f.runs + "'\nsleep 0.4\n" +
		"cat '" + filepath.Join(f.home, "catalog.json") + "'\n"
	if err := os.WriteFile(f.program, []byte(slow), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var wg sync.WaitGroup
	held := make([]*Held, 2)
	errs := make([]bytes.Buffer, 2)
	for i := range held {
		wg.Add(1)
		go func() {
			defer wg.Done()
			held[i] = f.request(ListEntry{ID: "gpt-6.1-sol"}).WriteIn(dir, &errs[i])
		}()
	}
	wg.Wait()
	for i, h := range held {
		if h == nil {
			t.Fatalf("launch %d: no menu:\n%s", i+1, errs[i].String())
		}
		defer h.Close()
	}
	if held[0].Path != held[1].Path {
		t.Errorf("two launches of one list hold %s and %s, want one menu", held[0].Path, held[1].Path)
	}
	if n := f.runCount(t); n != 1 {
		t.Errorf("two first launches at once ran the program %d times, want once: the second must wait "+
			"on the decision lock and reuse the first's menu", n)
	}
}

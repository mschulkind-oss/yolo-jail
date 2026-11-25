package unifiedhome

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

const homePath = "/workspace/.yolo/unified-home"

// composeFixture is a slice home and a unified home, each a real directory.
type composeFixture struct{ slice, unified string }

func newCompose(t *testing.T) composeFixture {
	t.Helper()
	return composeFixture{slice: tree(t), unified: tree(t)}
}

func (f composeFixture) run(t *testing.T, sliceDir, unifiedDir string, spec ComposeSpec, o Options) (ComposeReport, error) {
	t.Helper()
	spec.Label = "podman-self"
	spec.HomePath = homePath
	return Compose(open(t, f.slice), sliceDir, open(t, f.unified), unifiedDir, spec, o)
}

func linkTarget(t *testing.T, base, rel string) string {
	t.Helper()
	got, err := os.Readlink(filepath.Join(base, rel))
	if err != nil {
		t.Fatalf("%s is not a link: %v", rel, err)
	}
	return got
}

func isReal(base, rel string) bool {
	fi, err := os.Lstat(filepath.Join(base, rel))
	return err == nil && fi.Mode()&os.ModeSymlink == 0
}

func exists(base, rel string) bool {
	_, err := os.Lstat(filepath.Join(base, rel))
	return err == nil
}

func TestComposeLinksSharedChildrenAndKeepsGeneratedReal(t *testing.T) {
	f := newCompose(t)
	put(t, f.slice, ".claude/projects/-workspace/w.jsonl", "w")
	put(t, f.slice, ".claude/settings.json", "{}")
	rep, err := f.run(t, ".claude", ".claude", ComposeSpec{Generated: []string{"settings.json"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := linkTarget(t, f.slice, ".claude/projects"); got != homePath+"/.claude/projects" {
		t.Errorf("projects links to %q", got)
	}
	if got := read(t, f.unified, ".claude/projects/-workspace/w.jsonl"); got != "w" {
		t.Errorf("transcript in the unified home = %q", got)
	}
	if !isReal(f.slice, ".claude/settings.json") || exists(f.unified, ".claude/settings.json") {
		t.Error("a generated file must stay real in the slice and never reach the unified home")
	}
	if rep.Linked != 1 || rep.Declined {
		t.Errorf("report = %+v", rep)
	}
}

func TestComposePrefixCarveOutIsComposedRecursively(t *testing.T) {
	f := newCompose(t)
	put(t, f.slice, ".pi/agent/sessions/s.jsonl", "s")
	put(t, f.slice, ".pi/agent/npm/node_modules/x", "x")
	if _, err := f.run(t, ".pi", ".pi", ComposeSpec{Carved: []string{"agent/npm"}}, Options{}); err != nil {
		t.Fatal(err)
	}
	if !isReal(f.slice, ".pi/agent") {
		t.Fatal("agent must be a real directory: it is a prefix of a carve-out")
	}
	if got := linkTarget(t, f.slice, ".pi/agent/sessions"); got != homePath+"/.pi/agent/sessions" {
		t.Errorf("agent/sessions links to %q", got)
	}
	if !isReal(f.slice, ".pi/agent/npm") || read(t, f.slice, ".pi/agent/npm/node_modules/x") != "x" {
		t.Error("the carved agent/npm must stay real in the slice")
	}
	if exists(f.unified, ".pi/agent/npm") {
		t.Error("a carved path must never reach the unified home")
	}
	if read(t, f.unified, ".pi/agent/sessions/s.jsonl") != "s" {
		t.Error("the shared sessions did not move up")
	}
}

func TestComposeNeverAdoptsAUserLink(t *testing.T) {
	f := newCompose(t)
	outside := tree(t)
	put(t, outside, "secret", "keep")
	if err := os.MkdirAll(filepath.Join(f.slice, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.slice, ".claude/mine")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(t, ".claude", ".claude", ComposeSpec{}, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := linkTarget(t, f.slice, ".claude/mine"); got != outside {
		t.Errorf("the user's link was rewritten to %q", got)
	}
	if exists(f.unified, ".claude/mine") {
		t.Error("a user link must never be adopted into the unified home")
	}
	if read(t, outside, "secret") != "keep" {
		t.Error("the link's target was touched")
	}
}

func TestComposeLinksUnifiedChildrenAbsentFromTheSlice(t *testing.T) {
	f := newCompose(t)
	put(t, f.unified, ".claude/projects/a.jsonl", "a")
	put(t, f.unified, ".claude/settings.json", "from another setup")
	if err := os.MkdirAll(filepath.Join(f.slice, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(t, ".claude", ".claude", ComposeSpec{Generated: []string{"settings.json"}}, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := linkTarget(t, f.slice, ".claude/projects"); got != homePath+"/.claude/projects" {
		t.Errorf("projects links to %q", got)
	}
	if exists(f.slice, ".claude/settings.json") {
		t.Error("a generated name is never linked from the unified home")
	}
}

func TestComposeAdoptingAnExistingNameMergesAndKeepsBoth(t *testing.T) {
	f := newCompose(t)
	put(t, f.unified, ".claude/projects/a.jsonl", "old")
	put(t, f.slice, ".claude/projects/a.jsonl", "different")
	put(t, f.slice, ".claude/projects/b.jsonl", "b")
	rep, err := f.run(t, ".claude", ".claude", ComposeSpec{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if read(t, f.unified, ".claude/projects/b.jsonl") != "b" {
		t.Error("b.jsonl did not arrive")
	}
	if rep.KeptBoth != 1 {
		t.Errorf("KeptBoth = %d, want 1", rep.KeptBoth)
	}
	if got := linkTarget(t, f.slice, ".claude/projects"); got != homePath+"/.claude/projects" {
		t.Errorf("projects links to %q", got)
	}
}

func TestComposeDeclinesWhenTheSliceCannotHoldLinks(t *testing.T) {
	f := newCompose(t)
	put(t, f.slice, ".claude/projects/w.jsonl", "w")
	o := Options{Symlink: func(string, int, string) error { return syscall.EPERM }}
	rep, err := f.run(t, ".claude", ".claude", ComposeSpec{}, o)
	if err != nil {
		t.Fatalf("a decline must not be an error: %v", err)
	}
	if !rep.Declined || rep.DeclinedLine == "" {
		t.Fatalf("report = %+v, want a Declined result with its line", rep)
	}
	// Nothing is lost: the file is in the unified home, and a rerun on a slice that can hold links finishes it.
	if read(t, f.unified, ".claude/projects/w.jsonl") != "w" {
		t.Error("the transcript is not in the unified home")
	}
	if _, err := f.run(t, ".claude", ".claude", ComposeSpec{}, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := linkTarget(t, f.slice, ".claude/projects"); got != homePath+"/.claude/projects" {
		t.Errorf("after the rerun projects links to %q", got)
	}
}

func TestComposeIsIdempotent(t *testing.T) {
	f := newCompose(t)
	put(t, f.slice, ".pi/agent/sessions/s.jsonl", "s")
	put(t, f.slice, ".pi/agent/npm/x", "x")
	put(t, f.slice, ".pi/models.json", "m")
	spec := ComposeSpec{Carved: []string{"agent/npm"}}
	if _, err := f.run(t, ".pi", ".pi", spec, Options{}); err != nil {
		t.Fatal(err)
	}
	before := [2]map[string]string{snapshot(t, f.slice), snapshot(t, f.unified)}
	rep, err := f.run(t, ".pi", ".pi", spec, Options{})
	if err != nil {
		t.Fatal(err)
	}
	after := [2]map[string]string{snapshot(t, f.slice), snapshot(t, f.unified)}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a second run changed the trees:\nbefore %v\nafter  %v", before, after)
	}
	if rep.Moved != 0 || rep.Linked != 0 || rep.Declined {
		t.Errorf("a second run reported work: %+v", rep)
	}
}

func TestComposeLeavesALinkedSliceDirectory(t *testing.T) {
	f := newCompose(t)
	outside := tree(t)
	put(t, outside, "x", "x")
	if err := os.Symlink(outside, filepath.Join(f.slice, ".claude")); err != nil {
		t.Fatal(err)
	}
	rep, err := f.run(t, ".claude", ".claude", ComposeSpec{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Left) != 1 || exists(outside, "projects") {
		t.Errorf("a linked slice directory must be left alone: %+v", rep)
	}
}

func TestPlanComposeNamesEveryStep(t *testing.T) {
	file := func(n string) Entry { return Entry{Name: n, Kind: KindFile} }
	dir := func(n string) Entry { return Entry{Name: n, Kind: KindDir} }
	link := func(n string) Entry { return Entry{Name: n, Kind: KindLink} }
	slice := []Entry{dir("projects"), file("settings.json"), link("mine"), dir("agent"), {Name: "sock", Kind: KindOther}}
	unified := []Entry{dir("history"), dir("projects")}
	got := PlanCompose(slice, unified, []string{"settings.json"}, []string{"agent/npm"})
	want := []ComposeStep{
		{Name: "agent", Op: ComposeRecurse},
		{Name: "history", Op: ComposeLink},
		{Name: "projects", Op: ComposeAdopt},
		{Name: "sock", Op: ComposeLeave, Reason: "a socket, fifo or device"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("steps = %+v\nwant   %+v", got, want)
	}
}

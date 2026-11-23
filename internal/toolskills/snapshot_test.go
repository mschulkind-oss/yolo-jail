package toolskills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

type snapFixture struct {
	root, ws, installs string
}

func newFixture(t *testing.T) snapFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := snapFixture{root: root, ws: filepath.Join(root, "ws"), installs: filepath.Join(root, "mise", "installs")}
	for _, d := range []string{f.ws, f.installs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f snapFixture) opts() Options {
	return Options{Workspace: f.ws, InstallsDir: f.installs, Token: "tok-1"}
}

// skill makes installs/<tool>/<ver>/skills/<name>/SKILL.md and returns the Skill.
func (f snapFixture) skill(t *testing.T, tool, ver, name, body string) Skill {
	t.Helper()
	dir := filepath.Join(f.installs, strings.ReplaceAll(tool, ":", "-"), ver, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Skill{Name: name, Tool: tool, Version: ver, Path: dir}
}

func (f snapFixture) snapDir() string { return filepath.Join(f.ws, ".yolo", "tool-skills") }

func (f snapFixture) current(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.snapDir(), "current"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func mustPublish(t *testing.T, o Options, s ...Skill) Snapshot {
	t.Helper()
	snap, err := Publish(o, s)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func skippedNames(s Snapshot) []string {
	var n []string
	for _, k := range s.Skipped {
		n = append(n, k.Skill.Name)
	}
	return n
}

func TestPublishWritesDigestDirIndexAndCurrent(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "packslip:github.com/x/hk", "1.0", "hk-debug", "alpha")
	snap := mustPublish(t, f.opts(), a)
	if snap.Digest == "" || f.current(t) != snap.Digest {
		t.Fatalf("digest %q current %q", snap.Digest, f.current(t))
	}
	dir := filepath.Join(f.snapDir(), snap.Digest)
	body, err := os.ReadFile(filepath.Join(dir, "skills", "hk-debug", "SKILL.md"))
	if err != nil || string(body) != "alpha" {
		t.Fatalf("%q %v", body, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx struct {
		Token  string
		Digest string
		Skills []struct{ Tool, Version, Name string }
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	if idx.Token != "tok-1" || idx.Digest != snap.Digest || len(idx.Skills) != 1 ||
		idx.Skills[0].Tool != a.Tool || idx.Skills[0].Version != "1.0" || idx.Skills[0].Name != "hk-debug" {
		t.Fatalf("%s", raw)
	}
}

func TestDigestIsContentAddressedAndTokenIsNotInIt(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "t", "1", "s", "one")
	d1 := mustPublish(t, f.opts(), a).Digest
	o := f.opts()
	o.Token = "tok-2"
	if d2 := mustPublish(t, o, a).Digest; d2 != d1 {
		t.Fatalf("token changed the digest: %s %s", d1, d2)
	}
	raw, _ := os.ReadFile(filepath.Join(f.snapDir(), d1, "index.json"))
	if !strings.Contains(string(raw), "tok-2") {
		t.Fatalf("index keeps a stale token: %s", raw)
	}
	if err := os.WriteFile(filepath.Join(a.Path, "SKILL.md"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d3 := mustPublish(t, f.opts(), a).Digest; d3 == d1 {
		t.Fatal("changed bytes kept the digest")
	}
}

func TestCurrentIsReplacedLastAndAfterTheDirIsComplete(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "t", "1", "s", "one")
	old := mustPublish(t, f.opts(), a).Digest
	if err := os.WriteFile(filepath.Join(a.Path, "SKILL.md"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	testHookBeforeCurrent = func(dir string) {
		calls++
		if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
			t.Errorf("digest dir incomplete when current is replaced: %v", err)
		}
		if got := f.current(t); got != old {
			t.Errorf("current moved early: %s", got)
		}
	}
	defer func() { testHookBeforeCurrent = nil }()
	snap := mustPublish(t, f.opts(), a)
	if calls != 1 || f.current(t) != snap.Digest || snap.Digest == old {
		t.Fatalf("calls %d current %s", calls, f.current(t))
	}
	if _, err := os.Stat(filepath.Join(f.snapDir(), old)); err != nil {
		t.Fatalf("the previous snapshot is removed while a reader may hold it: %v", err)
	}
}

func TestSymlinkEscapeSkipsThatSkillOnly(t *testing.T) {
	f := newFixture(t)
	secret := filepath.Join(f.root, "secret")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := f.skill(t, "t", "1", "bad", "x")
	good := f.skill(t, "t", "1", "good", "y")
	for name, target := range map[string]string{"abs": secret, "rel": "../../../../../../../secret"} {
		if err := os.Symlink(target, filepath.Join(bad.Path, name)); err != nil {
			t.Fatal(err)
		}
	}
	snap := mustPublish(t, f.opts(), bad, good)
	if got := skippedNames(snap); len(got) != 1 || got[0] != "bad" || snap.Skipped[0].Reason == "" {
		t.Fatalf("%+v", snap.Skipped)
	}
	if _, err := os.Stat(filepath.Join(f.snapDir(), snap.Digest, "skills", "good", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.snapDir(), snap.Digest, "skills", "bad")); err == nil {
		t.Fatal("a skipped skill left bytes in the snapshot")
	}
}

func TestSymlinkInsideTheSkillIsCopiedAsAFile(t *testing.T) {
	f := newFixture(t)
	s := f.skill(t, "t", "1", "s", "body")
	if err := os.Symlink("SKILL.md", filepath.Join(s.Path, "alias.md")); err != nil {
		t.Fatal(err)
	}
	snap := mustPublish(t, f.opts(), s)
	p := filepath.Join(f.snapDir(), snap.Digest, "skills", "s", "alias.md")
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("%v %v", fi, err)
	}
}

func TestLinkSwappedInAfterTheWalkIsNotFollowed(t *testing.T) {
	f := newFixture(t)
	secret := filepath.Join(f.root, "secret")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := f.skill(t, "t", "1", "s", "body")
	testHookBeforeRead = func(path string) {
		if filepath.Base(path) == "SKILL.md" {
			_ = os.Remove(path)
			_ = os.Symlink(secret, path)
		}
	}
	defer func() { testHookBeforeRead = nil }()
	snap := mustPublish(t, f.opts(), s)
	if len(snap.Skipped) != 1 || len(snap.Skills) != 0 {
		t.Fatalf("%+v", snap)
	}
	_ = filepath.Walk(f.snapDir(), func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "SECRET") {
				t.Errorf("secret reached the snapshot at %s", p)
			}
		}
		return nil
	})
}

func TestFIFOSkipsTheSkillWithoutBlocking(t *testing.T) {
	f := newFixture(t)
	s := f.skill(t, "t", "1", "s", "body")
	if err := syscall.Mkfifo(filepath.Join(s.Path, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := f.skill(t, "t", "1", "ok", "fine")
	snap := mustPublish(t, f.opts(), s, ok)
	if got := skippedNames(snap); len(got) != 1 || got[0] != "s" {
		t.Fatalf("%+v", snap.Skipped)
	}
}

func TestSymlinkCycleSkipsTheSkill(t *testing.T) {
	f := newFixture(t)
	s := f.skill(t, "t", "1", "s", "body")
	if err := os.Symlink(".", filepath.Join(s.Path, "loop")); err != nil {
		t.Fatal(err)
	}
	two := f.skill(t, "t", "1", "two", "body")
	if err := os.Symlink("b", filepath.Join(two.Path, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a", filepath.Join(two.Path, "b")); err != nil {
		t.Fatal(err)
	}
	snap := mustPublish(t, f.opts(), s, two)
	if len(snap.Skipped) != 2 {
		t.Fatalf("%+v", snap.Skipped)
	}
}

func TestSizeAndEntryCapsApplyToTheWholeSnapshot(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "t", "1", "a", strings.Repeat("x", 60))
	b := f.skill(t, "t", "1", "b", strings.Repeat("y", 60))
	o := f.opts()
	o.MaxBytes = 100
	snap := mustPublish(t, o, a, b)
	if got := skippedNames(snap); len(got) != 1 || got[0] != "b" {
		t.Fatalf("bytes: %+v", snap.Skipped)
	}
	o = f.opts()
	o.MaxEntries = 3 // a is a directory and a file; b would make four
	snap = mustPublish(t, o, a, b)
	if got := skippedNames(snap); len(got) != 1 || got[0] != "b" {
		t.Fatalf("entries: %+v", snap.Skipped)
	}
	if len(snap.Skills) != 1 || snap.Skills[0].Name != "a" {
		t.Fatalf("%+v", snap.Skills)
	}
}

func TestVanishedAndOutsideInstallsAreSkippedAndNamed(t *testing.T) {
	f := newFixture(t)
	gone := Skill{Name: "gone", Tool: "t", Version: "1", Path: filepath.Join(f.installs, "t", "1", "nope")}
	outside := filepath.Join(f.root, "elsewhere", "s")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("x"), 0o644)
	out := Skill{Name: "out", Tool: "t", Version: "1", Path: outside}
	dotdot := Skill{Name: "dd", Tool: "t", Version: "1", Path: f.installs + "/../../elsewhere/s"}
	ok := f.skill(t, "t", "1", "ok", "x")
	snap := mustPublish(t, f.opts(), gone, out, dotdot, ok)
	if got := skippedNames(snap); len(got) != 3 {
		t.Fatalf("%+v", snap.Skipped)
	}
	if len(snap.Skills) != 1 || snap.Skills[0].Name != "ok" {
		t.Fatalf("%+v", snap.Skills)
	}
}

func TestInstallsSymlinkedVersionDirIsFollowed(t *testing.T) {
	f := newFixture(t)
	store := filepath.Join(f.root, "store", "hk")
	if err := os.MkdirAll(filepath.Join(store, "skills", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(store, "skills", "s", "SKILL.md"), []byte("x"), 0o644)
	if err := os.MkdirAll(filepath.Join(f.installs, "t"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(store, filepath.Join(f.installs, "t", "1")); err != nil {
		t.Fatal(err)
	}
	s := Skill{Name: "s", Tool: "t", Version: "1", Path: filepath.Join(f.installs, "t", "1", "skills", "s")}
	if snap := mustPublish(t, f.opts(), s); len(snap.Skipped) != 0 || len(snap.Skills) != 1 {
		t.Fatalf("%+v", snap)
	}
}

// listing is every path under root with its mode and size, except the workspace's .yolo tree and
// the workspace directory's own line (its mtime moves when .yolo first appears).
func listing(t *testing.T, root, ws string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || p == ws {
			return nil
		}
		if p == filepath.Join(ws, ".yolo") {
			return filepath.SkipDir
		}
		out = append(out, p+"|"+fi.Mode().String()+"|"+fi.ModTime().String())
		return nil
	})
	return out
}

func TestNothingIsWrittenOutsideTheSnapshotDirectory(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "t", "1", "s", "one")
	if err := os.WriteFile(filepath.Join(f.ws, "tracked.txt"), []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := strings.Join(listing(t, f.root, f.ws), "\n")
	mustPublish(t, f.opts(), a) // the default lock path must stay inside the directory too
	mustPublish(t, f.opts())    // and an empty replacement
	if after := strings.Join(listing(t, f.root, f.ws), "\n"); after != before {
		t.Fatalf("something outside .yolo/tool-skills changed:\n%s\n---\n%s", before, after)
	}
	ents, _ := os.ReadDir(filepath.Join(f.ws, ".yolo"))
	if len(ents) != 1 || ents[0].Name() != "tool-skills" {
		t.Fatalf("%v", ents)
	}
	ents, _ = os.ReadDir(f.snapDir())
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp") {
			t.Fatalf("scratch left behind: %s", e.Name())
		}
	}
}

func TestSymlinkedYoloDirIsRefused(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "t", "1", "s", "one")
	elsewhere := filepath.Join(f.root, "elsewhere")
	_ = os.MkdirAll(elsewhere, 0o755)
	if err := os.Symlink(elsewhere, filepath.Join(f.ws, ".yolo")); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(f.opts(), []Skill{a}); err == nil {
		t.Fatal("published through a link")
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
		t.Fatal("wrote through a link")
	}
}

func TestConcurrentPublishersSerializeOnOneLock(t *testing.T) {
	f := newFixture(t)
	var skills []Skill
	for _, n := range []string{"a", "b", "c", "d"} {
		skills = append(skills, f.skill(t, "t", "1", n, "body-"+n))
	}
	var wg sync.WaitGroup
	digests := make([]string, len(skills))
	for i := range skills {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Publish(f.opts(), skills[i:i+1])
			if err != nil {
				t.Error(err)
			}
			digests[i] = s.Digest
		}()
	}
	wg.Wait()
	cur := f.current(t)
	found := false
	for _, d := range digests {
		found = found || d == cur
	}
	if !found {
		t.Fatalf("current %s is no publisher's digest %v", cur, digests)
	}
	if _, err := os.Stat(filepath.Join(f.snapDir(), cur, "index.json")); err != nil {
		t.Fatal(err)
	}
}

// --- Refresh -------------------------------------------------------------------------

func miseListing(t *testing.T, skills ...Skill) (string, string) {
	t.Helper()
	if skills == nil {
		skills = []Skill{}
	}
	b, _ := json.Marshal(skills)
	marker := filepath.Join(t.TempDir(), "calls")
	return fakeMise(t, `echo "$*" >> `+marker+"\ncat <<'EOF'\n"+string(b)+"\nEOF\n"), marker
}

func TestRefreshPublishesTheSelectedSkillsInAllowlistOrder(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "tool-a", "1", "sa", "a")
	b := f.skill(t, "tool-b", "1", "sb", "b")
	c := f.skill(t, "tool-c", "1", "sc", "c") // not allowlisted
	m, _ := miseListing(t, c, b, a)
	rep := Refresh(context.Background(), f.opts(), []string{"tool-b", "tool-a"}, Runner{Mise: m})
	if !rep.Ran || rep.Err != nil || rep.Snapshot == nil {
		t.Fatalf("%+v", rep)
	}
	raw, _ := os.ReadFile(filepath.Join(f.snapDir(), f.current(t), "index.json"))
	var idx struct{ Skills []struct{ Name string } }
	_ = json.Unmarshal(raw, &idx)
	if len(idx.Skills) != 2 || idx.Skills[0].Name != "sb" || idx.Skills[1].Name != "sa" {
		t.Fatalf("%s", raw)
	}
}

func TestRefreshWithAnEmptyAllowlistRunsNothingAndWritesNothing(t *testing.T) {
	f := newFixture(t)
	m, marker := miseListing(t)
	for _, al := range [][]string{nil, {}} {
		rep := Refresh(context.Background(), f.opts(), al, Runner{Mise: m})
		if rep.Ran || rep.Err != nil {
			t.Fatalf("%+v", rep)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("mise was run")
	}
	if _, err := os.Stat(filepath.Join(f.ws, ".yolo")); err == nil {
		t.Fatal("a file was written")
	}
}

func TestAFailedRefreshKeepsThePreviousSnapshotAndAnEmptyOneReplacesIt(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "tool-a", "1", "sa", "a")
	m, _ := miseListing(t, a)
	rep := Refresh(context.Background(), f.opts(), []string{"tool-a"}, Runner{Mise: m})
	if rep.Snapshot == nil {
		t.Fatalf("%+v", rep)
	}
	first := rep.Snapshot.Digest
	bad := fakeMise(t, "echo boom >&2; exit 3")
	rep = Refresh(context.Background(), f.opts(), []string{"tool-a"}, Runner{Mise: bad})
	if rep.Err == nil || rep.Snapshot != nil || f.current(t) != first {
		t.Fatalf("%+v current %s", rep, f.current(t))
	}
	empty, _ := miseListing(t)
	rep = Refresh(context.Background(), f.opts(), []string{"tool-a"}, Runner{Mise: empty})
	if rep.Err != nil || rep.Snapshot == nil || f.current(t) == first || len(rep.Snapshot.Skills) != 0 {
		t.Fatalf("%+v", rep)
	}
	raw, _ := os.ReadFile(filepath.Join(f.snapDir(), f.current(t), "index.json"))
	if !strings.Contains(string(raw), "tok-1") {
		t.Fatalf("%s", raw)
	}
}

// --- RunStep -------------------------------------------------------------------------

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRunStepEmptyAllowlistDoesNothing(t *testing.T) {
	f := newFixture(t)
	m, marker := miseListing(t)
	for _, v := range []string{"", "[]", " [ ] "} {
		var out strings.Builder
		rc := RunStep(context.Background(), envOf(map[string]string{"YOLO_MISE_SKILL_TOOLS": v, "YOLO_TOOL_SKILLS_TOKEN": "t"}),
			[]string{f.ws}, Runner{Mise: m}, &out)
		if rc != 0 || out.Len() != 0 {
			t.Fatalf("%q rc %d out %q", v, rc, out.String())
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("mise was run")
	}
}

func TestRunStepPublishesFromTheEnvironmentAndNeverFails(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "tool-a", "1", "sa", "a")
	m, _ := miseListing(t, a)
	env := map[string]string{
		"YOLO_MISE_SKILL_TOOLS":  `["tool-a"]`,
		"YOLO_TOOL_SKILLS_TOKEN": "tok-9",
		"MISE_DATA_DIR":          filepath.Dir(f.installs),
	}
	var out strings.Builder
	if rc := RunStep(context.Background(), envOf(env), []string{f.ws}, Runner{Mise: m}, &out); rc != 0 {
		t.Fatalf("rc %d %s", rc, out.String())
	}
	raw, err := os.ReadFile(filepath.Join(f.snapDir(), f.current(t), "index.json"))
	if err != nil || !strings.Contains(string(raw), "tok-9") {
		t.Fatalf("%s %v", raw, err)
	}
	bad := fakeMise(t, "exit 4")
	out.Reset()
	if rc := RunStep(context.Background(), envOf(env), []string{f.ws}, Runner{Mise: bad}, &out); rc != 0 ||
		strings.Count(out.String(), "\n") != 1 || !strings.HasPrefix(out.String(), "yolo: ") {
		t.Fatalf("rc %d %q", rc, out.String())
	}
	out.Reset()
	env["YOLO_MISE_SKILL_TOOLS"] = "not json"
	if rc := RunStep(context.Background(), envOf(env), []string{f.ws}, Runner{Mise: m}, &out); rc != 0 || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("rc %d %q", rc, out.String())
	}
}

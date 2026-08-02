package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// filesMounts extracts the -v pairs a `files` contribution produced. It matches on the
// destination shape rather than the source, because the source is a t.TempDir() path the
// caller cannot predict.
func filesMounts(argv []string, destRel string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && strings.HasSuffix(argv[i+1], ":/home/agent/"+destRel+":ro") {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// filesPack writes a pack tree with one `files` contribution and loads it the way a run
// does. contents maps a path under the `from` dir to its bytes; an EMPTY map makes the
// `from` path a single FILE instead of a directory (the Apple Container case).
func filesPack(t *testing.T, name, from, into string, contents map[string]string) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	if len(contents) == 0 {
		if err := os.WriteFile(filepath.Join(root, from), []byte("single-file tree\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		for rel, body := range contents {
			full := filepath.Join(root, from, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest := `{"name":"` + name + `","contributes":[` +
		`{"kind":"files","from":"` + from + `","into":"` + into + `"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, name, true)
	if len(problems) != 0 {
		t.Fatalf("loading the %s fixture pack: %v", name, problems)
	}
	return p
}

// TestAssemblePackFilesMount is the regression for plan finding N1: `files` shipped
// INERT. It passed `pack lint`, printed a footprint claim ("read-only tree"), was refused
// by name at the host — and was silently dropped in a jail, because assemble.go switched
// on KindSkills and KindBriefing and never on KindFiles. This asserts the bind the
// footprint and the host refusal string both already promised.
func TestAssemblePackFilesMount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	pack := filesPack(t, "fkdir", "files", ".claude/fkdir", map[string]string{
		"file-suggestion.sh": "#!/bin/sh\nfd .\n",
	})
	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.packs = append(in.packs, pack)

	got := filesMounts(o.assembleRunCmd(in), ".claude/fkdir")
	want := []string{filepath.Join(pack.Root, "files") + ":/home/agent/.claude/fkdir:ro"}
	if !slices.Equal(got, want) {
		t.Errorf("`files` mounts = %v, want %v", got, want)
	}
}

// TestAssemblePackFilesNoneMatchesGolden pins the no-`files` case against the frozen
// argv: delivering the kind must be a pure no-op for every pack that does not declare it
// (none of the six shipped packs does).
func TestAssemblePackFilesNoneMatchesGolden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	got := o.assembleRunCmd(relocationInput(t, "podman", "/ws/.yolo/home", nil))
	if !slices.Equal(got, podmanLinuxGolden(home)) {
		t.Errorf("argv drifted from the golden with no `files` contribution:\ngot:  %v\nwant: %v",
			got, podmanLinuxGolden(home))
	}
}

// TestAssemblePackFilesAbsentSourceWarns: an `only`/`exclude` filter (or a wrong `from`)
// can leave nothing staged at the declared path. Mounting a missing source kills the
// whole container with a bare "statfs …: no such file or directory", so the launch skips
// it — but LOUDLY. A silent skip here would just reinstate the bug this phase fixes.
func TestAssemblePackFilesAbsentSourceWarns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	var buf strings.Builder
	o.Stdout = &buf

	// A pack whose `from` names a path its tree does not carry.
	root := t.TempDir()
	manifest := `{"name":"ghost","contributes":[{"kind":"files","from":"files","into":".ghost"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "ghost", true)
	if len(problems) != 0 {
		t.Fatalf("loading the ghost pack: %v", problems)
	}
	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.packs = append(in.packs, p)

	got := o.assembleRunCmd(in)
	if mounts := filesMounts(got, ".ghost"); mounts != nil {
		t.Errorf("an absent `files` source must not be mounted (podman would abort the "+
			"container on a missing bind source), got %v", mounts)
	}
	warning := buf.String()
	for _, want := range []string{"ghost", ".ghost", "only/exclude"} {
		if !strings.Contains(warning, want) {
			t.Errorf("skip warning missing %q; got:\n%s", want, warning)
		}
	}
}

// TestAssemblePackFilesSingleFileMaterializesOnAppleContainer is the AC half of the
// same-class bug the rest of this work fixes: Apple Container cannot bind-mount a single
// FILE (apple/container#1089), which is why yolo-user-env.sh and every briefing route
// through acMaterialize. A `files` contribution naming one file has to take the same
// road, or it silently vanishes on that backend.
func TestAssemblePackFilesSingleFileMaterializesOnAppleContainer(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.IsMacOS, o.IsLinux = true, false

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	// nil contents → `from` is a single file, not a dir.
	pack := filesPack(t, "onefile", "file-suggestion.sh", ".claude/file-suggestion.sh", nil)
	in := relocationInput(t, "container", wsState, nil)
	in.packs = append(in.packs, pack)

	got := o.assembleRunCmd(in)

	// Materialized under ws_state (which AC mounts wholesale at /home/agent).
	materialized := filepath.Join(wsState, ".claude", "file-suggestion.sh")
	if b, err := os.ReadFile(materialized); err != nil || string(b) != "single-file tree\n" {
		t.Errorf("single-file `files` not materialized into ws_state: err=%v content=%q",
			err, string(b))
	}
	// And NOT single-file-mounted — that is the AC bug being avoided.
	if mounts := filesMounts(got, ".claude/file-suggestion.sh"); mounts != nil {
		t.Errorf("AC path must NOT single-file-mount a `files` contribution: %v", mounts)
	}
}

// TestAssemblePackFilesSingleFileMountsOnPodman is the other side of the AC split: podman
// binds a single file fine, so it must still get a real mount rather than a copy (a copy
// would silently stop tracking edits to the pack's tree).
func TestAssemblePackFilesSingleFileMountsOnPodman(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	pack := filesPack(t, "onefile", "file-suggestion.sh", ".claude/file-suggestion.sh", nil)
	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.packs = append(in.packs, pack)

	got := filesMounts(o.assembleRunCmd(in), ".claude/file-suggestion.sh")
	want := []string{filepath.Join(pack.Root, "file-suggestion.sh") +
		":/home/agent/.claude/file-suggestion.sh:ro"}
	if !slices.Equal(got, want) {
		t.Errorf("single-file `files` mount = %v, want %v", got, want)
	}
}

// TestPackFilesCollisionNamesBothPacks is plan item 6.2: the assembler emits one bind per
// contribution with NO dedup by destination, so two packs sharing an `into` reach podman
// as "duplicate mount destination" — a boot failure naming neither pack. `files` is
// CombineExclusive, so a second claimant is already a footprint violation; it must be a
// pre-flight error, with BOTH pack names in it.
func TestPackFilesCollisionNamesBothPacks(t *testing.T) {
	a := filesPack(t, "alpha", "files", ".shared/tree", map[string]string{"a.txt": "a\n"})
	b := filesPack(t, "beta", "files", ".shared/tree", map[string]string{"b.txt": "b\n"})

	conflicts := packDestConflicts([]*packload.Pack{a, b}, packdecl.KindFiles)
	if len(conflicts) != 1 {
		t.Fatalf("want exactly one conflict for one shared `into`, got %d: %v",
			len(conflicts), conflicts)
	}
	msg := conflicts[0]
	for _, want := range []string{"alpha", "beta", ".shared/tree", "duplicate-mount-destination"} {
		if !strings.Contains(msg, want) {
			t.Errorf("conflict message missing %q — a boot-time podman error names neither "+
				"pack, which is the whole reason this check exists; got:\n%s", want, msg)
		}
	}
}

// A single pack claiming one path twice is the SAME fatal duplicate mount, so it is
// reported too — with wording that does not invent a second pack.
func TestPackFilesCollisionWithinOnePack(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "two"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"selfish","contributes":[` +
		`{"kind":"files","from":"one","into":".dup"},` +
		`{"kind":"files","from":"two","into":".dup"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "selfish", true)
	if len(problems) != 0 {
		t.Fatalf("loading the selfish pack: %v", problems)
	}

	conflicts := packDestConflicts([]*packload.Pack{p}, packdecl.KindFiles)
	if len(conflicts) != 1 {
		t.Fatalf("want one conflict, got %d: %v", len(conflicts), conflicts)
	}
	if !strings.Contains(conflicts[0], "selfish") || strings.Contains(conflicts[0], "packs ") {
		t.Errorf("a one-pack self-collision must name that pack without implying a second; got:\n%s",
			conflicts[0])
	}
}

// Distinct `into` paths must NOT collide — the check has to stay narrow enough that two
// packs each owning their own tree is the ordinary case.
func TestPackFilesDistinctDestsDoNotCollide(t *testing.T) {
	a := filesPack(t, "alpha", "files", ".alpha/tree", map[string]string{"a.txt": "a\n"})
	b := filesPack(t, "beta", "files", ".beta/tree", map[string]string{"b.txt": "b\n"})

	if conflicts := packDestConflicts([]*packload.Pack{a, b}, packdecl.KindFiles); len(conflicts) != 0 {
		t.Errorf("distinct `into` paths must not collide, got: %v", conflicts)
	}
}

// The check is keyed on a KIND so it can be reused, and it must NOT be silently applied to
// skills: two `skills` contributions sharing an `into` is the same podman failure but a
// DIFFERENT bug (skills are a designed merge, so the fix is mount dedup, not a collision
// error). Deliberately out of scope — plan OQ-C. This pins the scope so a later reader
// does not "helpfully" widen the caller and turn a legal merge into a launch failure.
func TestPackDestConflictsIsNotWiredForSkills(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude", "copilot"]`)

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-files-scope")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	// Both packs declare a `skills` contribution (at different `into` paths today). Assert
	// the KIND-keyed helper still finds nothing for files — i.e. no shipped pack trips the
	// new pre-flight — while making it explicit that skills is not routed through it.
	if conflicts := packDestConflicts(loaded, packdecl.KindFiles); len(conflicts) != 0 {
		t.Errorf("no shipped pack declares `files`, so the pre-flight must be silent; got: %v",
			conflicts)
	}
}

// TestStagePacksRefusesFilesCollision is the pre-flight at its real call site: a
// collision must fail the LAUNCH, before podman is invoked, with both names. Fail-closed
// matches the rest of stagePacks — silently mounting whichever claim podman accepted
// would let one pack's content shadow the other's.
func TestStagePacksRefusesFilesCollision(t *testing.T) {
	home := packHome(t)

	// Two local packs, same `into`.
	var dirs []string
	for _, name := range []string{"alpha", "beta"} {
		root := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(filepath.Join(root, "files"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "files", name+".txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest := `{"name":"` + name + `","contributes":[` +
			`{"kind":"files","from":"files","into":".shared/tree"}]}`
		if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, root)
	}
	writeUserPacks(t, home,
		`["file://`+dirs[0]+`", "file://`+dirs[1]+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, _, _, err := o.stagePacks("yolo-test-files-collide")
	if err == nil {
		t.Fatal("two packs claiming one `files` destination must fail the launch — " +
			"podman would otherwise reject the second bind with an error naming neither pack")
	}
	for _, want := range []string{"alpha", "beta", ".shared/tree"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("stagePacks error missing %q; got: %v", want, err)
		}
	}
}

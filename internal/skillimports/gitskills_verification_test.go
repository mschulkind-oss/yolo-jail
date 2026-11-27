package skillimports_test

// Follow-up verification for Git sourced skills (docs/plans/git-skills-aws-verification.md):
// the composed whole-payload read counts of N7 (a fresh launch, an attach, host observe)
// and the two source-only P2 notes, reproduced as passing tests that pin what happens.
//
// This is an external test package so it may drive packload, packsrc and jailcontent
// together, which an in-package test of the import resolver cannot.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func verifyGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// payloadOrigin is a synthetic repository holding one skill of about `size` bytes.
func payloadOrigin(t *testing.T, size int64) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	remote := t.TempDir()
	verifyGit(t, remote, "init", "-q", "-b", "main")
	const files = 8
	each := int(size / files)
	for i := 0; i < files; i++ {
		blob := bytes.Repeat([]byte(fmt.Sprintf("file-%02d\n", i)), each/8+1)[:each]
		full := filepath.Join(remote, "skills/chosen/data", fmt.Sprintf("%02d.bin", i))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(remote, "skills/chosen/SKILL.md"), []byte("---\nname: chosen\n---\nmeasured\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verifyGit(t, remote, "add", "--all")
	verifyGit(t, remote, "commit", "-q", "-m", "payload")
	return remote
}

func readBytes(t *testing.T) int64 {
	t.Helper()
	raw, err := os.ReadFile("/proc/self/io")
	if err != nil {
		t.Skipf("no /proc/self/io: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "rchar: "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Skip("no rchar in /proc/self/io")
	return 0
}

func measuredSize(t *testing.T) int64 {
	if v := os.Getenv("YOLO_N7_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			t.Fatalf("YOLO_N7_PAYLOAD_BYTES=%q", v)
		}
		return n
	}
	return 16 << 20
}

func sourcedPack(name, root, source string) *packload.Pack {
	return &packload.Pack{Name: name, Root: root, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindSkills, Source: source, As: "alias"},
	}}}
}

// TestSourcedSkillComposedReadCountsAndNestedRecordRoot runs the whole sourced-skill path
// the launch, the attach and the host observer use, over ONE payload, and counts the
// whole-payload reads of each stage. The staged pack sits five directories below the
// tree's record root: the second P2 note was that such a pack's record-root lookup could
// miss, and attach reading the row back from the record proves it does not.
func TestSourcedSkillComposedReadCountsAndNestedRecordRoot(t *testing.T) {
	size := measuredSize(t)
	remote := payloadOrigin(t, size)
	store := &packsrc.Store{Dir: t.TempDir(), Env: append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")}
	testsupport.UnsealOnCleanup(t, store.Dir)
	lock := filepath.Join(t.TempDir(), packsrc.SkillsLockName)
	opts := packsrc.SkillResolveOptions{LockPath: lock, Now: func() time.Time { return time.Unix(1800000000, 0) }}
	source := "git+file://" + remote + "//skills/chosen?ref=main"
	tree := t.TempDir()
	nested := filepath.Join(tree, "a", "b", "c", "d", "e", "owner")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	pack := sourcedPack("owner", nested, source)

	// Fresh launch: resolve (fetch, plan, publish) and stage the payload into the launch tree.
	r0 := readBytes(t)
	results := packload.ResolveSelectedSkillPayloads([]*packload.Pack{pack}, store, opts, true)
	r1 := readBytes(t)
	if len(results) != 1 || results[0].State != packsrc.SkillReady {
		t.Fatalf("resolve: %+v", results)
	}
	if err := packload.StageSkillPayloads([]*packload.Pack{pack}, tree, tree); err != nil {
		t.Fatal(err)
	}
	r2 := readBytes(t)
	if err := packload.WritePackTreeRecord(tree, []*packload.Pack{pack}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = packsrc.RemoveSealedSkillTree(filepath.Dir(tree), filepath.Join(tree, packload.SkillPayloadTreeDir))
	})
	payload := results[0].Plan
	var retained int64
	for _, e := range payload.Entries {
		retained += int64(len(e.Data))
	}

	// Attach: read the running jail's record back and re-validate each recorded row.
	r3 := readBytes(t)
	entries, recorded, err := packload.ReadPackTreeRecord(tree)
	if err != nil || !recorded || len(entries) != 1 {
		t.Fatalf("record: %v %v %+v", entries, recorded, err)
	}
	if entries[0].Dir != "a/b/c/d/e/owner" {
		t.Fatalf("record dir %q: nested pack not recorded relative to the tree", entries[0].Dir)
	}
	fresh := sourcedPack("owner", nested, source)
	if err := entries[0].HydrateSourcedSkills(tree, fresh); err != nil {
		t.Fatalf("a nested staged pack's record did not hydrate: %v", err)
	}
	r4 := readBytes(t)
	rows := fresh.SourcedSkillRows()
	if len(rows) != 1 || rows[0].Plan == nil || rows[0].PayloadRoot != filepath.Join(tree, packload.SkillPayloadTreeDir, filepath.Base(rows[0].PayloadRoot)) {
		t.Fatalf("hydrated row: %+v", rows)
	}

	// Host observe: the read-only resolver, no lock, fetch or write.
	observer := sourcedPack("owner", t.TempDir(), source)
	r5 := readBytes(t)
	observed := packload.ResolveSelectedSkillPayloads([]*packload.Pack{observer}, store, opts, false)
	r6 := readBytes(t)
	if len(observed) != 1 || observed[0].State != packsrc.SkillReady {
		t.Fatalf("observe: %+v", observed)
	}

	count := func(name string, bytesRead int64, want int) {
		t.Helper()
		slack := retained/20 + 2<<20
		n := int(bytesRead / retained)
		if bytesRead-int64(n)*retained > slack || n != want {
			t.Errorf("%s: read %d bytes = %.2f payloads, baseline %d", name, bytesRead, float64(bytesRead)/float64(retained), want)
		}
		t.Logf("N7 %-34s %d whole-payload reads (%d bytes, payload %d)", name, n, bytesRead, retained)
	}
	count("fresh launch: resolve", r1-r0, 2)
	count("fresh launch: stage into the tree", r2-r1, 2)
	count("attach: hydrate one recorded row", r4-r3, 1)
	count("host observe: ResolveSelected(write=false)", r6-r5, 2)
	t.Logf("N7 retained by one resolved plan: %d bytes; the launch keeps it and, on attach, a second plan per row", retained)
	_ = results
}

// TestUnresolvedBuiltinCollisionIsRefusedByDeclarationAndOnlyDryRunDropsIt reproduces the
// first P2 note. A sourced row naming a built-in skill collides by DECLARATION, resolved or
// not. The dry-run briefing pass in run.refreshJailBriefingsMode filters rows that are not
// ready out of the source list before it composes, so over THAT list no collision exists.
// The refusal is therefore carried entirely by the earlier pre-flight over every declared
// row (run.checkSkillCollisions, in stagePacks), which this pins from both sides.
func TestUnresolvedBuiltinCollisionIsRefusedByDeclarationAndOnlyDryRunDropsIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	names := jailcontent.BuiltinSkillBareNames()
	if len(names) == 0 {
		t.Fatal("no built-in skill names")
	}
	targets := []jailcontent.SkillTarget{{Staging: "skills-selected", Dest: ".fixture/skills", Agent: "fixture"}}
	for _, name := range names {
		unresolved := &packload.SourcedSkill{Row: 0, By: packdecl.Contribution{Kind: packdecl.KindSkills, Source: "git+https://example.invalid/r.git//skills/x?ref=main", As: name}}
		declared := []jailcontent.PackSkillSource{{Pack: "selected", Registration: unresolved}}
		if err := jailcontent.SkillCollisionError(declared, targets); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("built-in %q: an unresolved declaration must collide: %v", name, err)
		}
		// The dry-run filter, spelled as prepare.go spells it: keep a source with no
		// registration, a withheld one, or a READY one.
		var ready []jailcontent.PackSkillSource
		for _, source := range declared {
			if source.Registration == nil || source.Registration.Withheld != "" || source.Registration.Outcome.State == packsrc.SkillReady {
				ready = append(ready, source)
			}
		}
		if len(ready) != 0 {
			t.Fatalf("filter kept an unresolved row: %+v", ready)
		}
		if err := jailcontent.SkillCollisionError(ready, targets); err != nil {
			t.Fatalf("filtered list should hold no collision, got %v", err)
		}
	}
}

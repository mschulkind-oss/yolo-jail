package cli

// packbaresteps_test.go runs every command the pack-naming messages name as a next step through
// the real `yolo config` path, and checks the entry it names is then delivered bare (pack-naming
// second review, majors 1 and 2): a source key that is no plain word (`skills.git`, which main's
// migrator made from a repository URL, and one holding a space), a plain-string `packs` entry, a
// table entry, and the `yolo pack add` handle that is not in the config yet.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

var stepCommand = regexp.MustCompile("`(yolo config [^`]*)`")

// runStep splits the one `yolo config …` command in text as a shell would and runs it through the
// config verb, asserting the write landed.
func runStep(t *testing.T, text string) {
	t.Helper()
	m := stepCommand.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no `yolo config …` command in %q", text)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not on PATH")
	}
	raw, err := exec.Command("bash", "-c", `f() { printf '%s\0' "$@"; }; f `+strings.TrimPrefix(m[1], "yolo ")).Output()
	if err != nil {
		t.Fatalf("the step does not parse as a shell command: %v\n%s", err, m[1])
	}
	args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if args[0] != "config" {
		t.Fatalf("not a config command: %q", args)
	}
	rc, out, errs := runConfigVerb(t, args[1:]...)
	assertPublicPostWriteCheck(t, rc, out, errs)
	if !strings.Contains(out, "Written") {
		t.Fatalf("%s was refused: rc=%d stdout=%q stderr=%q", m[1], rc, out, errs)
	}
}

// entryNamed is the loaded entry with handle name, failing on any load problem.
func entryNamed(t *testing.T, name string) config.PackEntry {
	t.Helper()
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) != 0 {
		t.Fatalf("the config does not load cleanly: %v %v", err, problems)
	}
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entry %s among %+v", name, entries)
	return config.PackEntry{}
}

func mkPackDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"contributes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTheKeepNamesStepForAnOddSourceKeyRunsAndMarksItBare(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"skills.git", "{ path = '%s' }"},
		{"my tools", "'%s'"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			src := t.TempDir()
			mkPackDir(t, filepath.Join(src, "x"))
			value := strings.ReplaceAll(tc.value, "%s", filepath.ToSlash(src))
			genericFixture(t, "packs = [\""+tc.key+"/x\"]\n\n[pack_sources]\n\""+tc.key+"\" = "+value+"\n",
				configdoc.TOML, "# keep project\n")
			_, problems, err := config.LoadPackEntries()
			if err != nil || len(problems) != 1 {
				t.Fatalf("a key composing no namespace word must be refused once: %v %v", err, problems)
			}
			runStep(t, problems[0])
			if e := entryNamed(t, tc.key+"/x"); e.Namespace != "" || e.KeepNamesStep != "" {
				t.Errorf("after the step the pack is not bare: %+v", e)
			}
		})
	}
}

func TestTheEntryStepsRunAndMarkTheEntry(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		accept      bool
	}{
		{"string keep", `"local/a"`, false},
		{"table keep", `{ pack = "local/a" }`, false},
		{"string accept", `"local/a"`, true},
		{"table accept", `{ pack = "local/a" }`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, _ := genericFixture(t, "packs = [\"local/z\", "+tc.entry+"]\n", configdoc.TOML, "# keep project\n")
			mkPackDir(t, filepath.Join(filepath.Dir(user.Path), "packs", "a"))
			mkPackDir(t, filepath.Join(filepath.Dir(user.Path), "packs", "z"))
			before := entryNamed(t, "local/a")
			if before.Namespace != "a" || before.KeepNamesStep == "" || before.AcceptNamesStep == "" {
				t.Fatalf("an unmarked local entry must default to its own namespace with both steps: %+v", before)
			}
			step := before.KeepNamesStep
			if tc.accept {
				step = before.AcceptNamesStep
			}
			if !strings.Contains(step, "packs[1]") {
				t.Errorf("the step does not name the entry's index: %s", step)
			}
			runStep(t, step)
			after := entryNamed(t, "local/a")
			wantNS := ""
			if tc.accept {
				wantNS = "a"
			}
			if after.Namespace != wantNS || after.KeepNamesStep != "" {
				t.Errorf("after %s the entry is %+v, want namespace %q and no step", step, after, wantNS)
			}
		})
	}
}

// `yolo pack add` checks a handle before it is in the config, so its step adds the table.
func TestThePackAddStepAddsTheEntryBare(t *testing.T) {
	user, _ := genericFixture(t, "packs = []\n", configdoc.TOML, "# keep project\n")
	root := filepath.Join(filepath.Dir(user.Path), "packs")
	mkPackDir(t, filepath.Join(root, "c"))
	ref, err := packsrc.ParsePackReference("local/c", nil, root, user.Root, "pack handle", nil)
	if err != nil {
		t.Fatal(err)
	}
	runStep(t, ref.KeepNamesStep)
	if e := entryNamed(t, "local/c"); e.Namespace != "" {
		t.Errorf("the added entry is not bare: %+v", e)
	}
}

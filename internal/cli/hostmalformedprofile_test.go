package cli

// hostmalformedprofile_test.go pins what `yolo host -- <agent>` says when the profile it selects
// is declared ONLY by a pack it cannot use — since NS-D14, one whose manifest has problems.
//
// The launch used to compose without such a pack, so the profile was undeclared at that launch and
// OQ-CS6 refused it, first as "no profile named "bp" is declared" about a profile a selected pack's
// manifest DOES declare, then naming the pack. Since NC-D5 the launch refuses the pack set itself
// before any profile is resolved, as a jail launch refuses the same config: the refusal names the
// pack and its problem, and never says the profile is undeclared.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostExecRun runs `yolo host [flags] -- <bin>` through hostMain with the exec replaced, in the
// home a fixture already set up, and reports the exit code, whether the exec was reached, and
// stderr.
func hostExecRun(t *testing.T, bin string, flags ...string) (int, bool, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bin")
	writeFile(t, filepath.Join(dir, bin), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(dir, bin), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	reached := false
	orig := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { reached = true; return nil }
	t.Cleanup(func() { hostSyscallExec = orig })
	var out, errw bytes.Buffer
	rc := hostMain(append(append([]string{}, flags...), "--", bin), &out, &errw, false, nil)
	return rc, reached, errw.String()
}

// badProfileContribs is a provider claude can reach plus the profile "bp" selecting it, both
// declared by the fixture pack "bad".
const badProfileContribs = `{"kind":"provider","name":"badprov",` +
	`"endpoints":{"anthropic":{"base_url":"https://bad.example"}}},` +
	`{"kind":"profile","name":"bp","provider":"badprov"},`

// THE REFUSAL NAMES THE PACK, with its problem and the lint remedy, whether `profile` or a
// typed -p selects the profile only it declares. The control is the same pack without the
// problem, whose profile launches.
func TestHostLaunchNamesTheMalformedPackDeclaringTheSelectedProfile(t *testing.T) {
	for _, via := range []string{"profile", "-p"} {
		for _, malformed := range []bool{true, false} {
			extra, flags := `,"profile":{"claude":"bp"}`, []string(nil)
			if via == "-p" {
				extra, flags = "", []string{"-p", "bp"}
			}
			malformedPackHome(t, badProfileContribs, malformed, extra)
			rc, reached, errw := hostExecRun(t, "claude", flags...)
			if !malformed {
				if rc != 0 || !reached {
					t.Fatalf("%s fixture control: a clean pack's profile must launch; rc=%d\n%s", via, rc, errw)
				}
				continue
			}
			if rc == 0 || reached {
				t.Fatalf("%s: the launch composed a profile only a refused pack declares; rc=%d\n%s",
					via, rc, errw)
			}
			for _, want := range []string{"bad:", malformedProblem, "yolo pack lint", "a jail launch refuses"} {
				if !strings.Contains(errw, want) {
					t.Errorf("%s: the refusal must contain %q:\n%s", via, want, errw)
				}
			}
			if strings.Contains(errw, "no profile named") {
				t.Errorf("%s: the refusal says nothing declares a profile the pack declares:\n%s", via, errw)
			}
		}
	}
}

// A MANIFEST THAT DOES NOT DECODE refuses the launch the same way, naming the pack; with no
// unusable pack, a profile nothing declares keeps the plain undeclared message.
//
// "Does not decode" is a value of the wrong type: one every build refuses. A field from a newer
// yolo used to stand in for it here, and no longer does — a host launch reads such a pack and names
// what it skips (docs/design/patched-forks.md PF-D60, pinned by
// TestHostLaunchRunsAPackWithAFieldFromANewerYolo).
func TestHostLaunchNamesAnUnusablePackBeforeAnUndeclaredProfile(t *testing.T) {
	for _, broken := range []bool{true, false} {
		_, dir := malformedPackHome(t, "", false, `,"profile":{"claude":"bp"}`)
		if broken {
			writeFile(t, filepath.Join(dir, "pack.json"),
				`{"name":"bad","description":1,"contributes":[`+
					strings.TrimSuffix(badProfileContribs, ",")+`]}`)
		}
		rc, reached, errw := hostExecRun(t, "claude")
		if rc == 0 || reached {
			t.Fatalf("broken=%v: bp is declared by nothing this launch reads; rc=%d\n%s", broken, rc, errw)
		}
		undeclared := strings.Contains(errw, `no profile named "bp"`)
		if undeclared == broken {
			t.Errorf("broken=%v: the undeclared message = %v:\n%s", broken, undeclared, errw)
		}
		names := strings.Contains(errw, "✗ bad:")
		if names != broken {
			t.Errorf("broken=%v: the refusal names the unusable pack = %v:\n%s", broken, names, errw)
		}
	}
}

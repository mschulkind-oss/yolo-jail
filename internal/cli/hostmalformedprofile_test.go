package cli

// hostmalformedprofile_test.go pins what `yolo host -- <agent>` says when the profile it selects
// is declared ONLY by a pack it cannot use — since NS-D14, one whose manifest has problems.
//
// The launch composes without such a pack (loadedHostPacks), so the profile is undeclared at this
// launch and OQ-CS6 refuses it, as a jail launch refuses the same config. What was wrong is the
// cause the refusal named: "no profile named "bp" is declared — a profile name must be declared
// by a selected pack's manifest …", about a profile a selected pack's manifest DOES declare. The
// refusal now names that pack and its problems.

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

// THE REFUSAL NAMES THE PACK THAT DECLARES THE PROFILE, with its problem and the lint remedy,
// whether `use_profiles` or a typed -p selects it. The control is the same pack without the
// problem, whose profile launches.
func TestHostLaunchNamesTheMalformedPackDeclaringTheSelectedProfile(t *testing.T) {
	for _, via := range []string{"use_profiles", "-p"} {
		for _, malformed := range []bool{true, false} {
			extra, flags := `,"use_profiles":{"claude":"bp"}`, []string(nil)
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
			for _, want := range []string{`"bp"`, "declared by pack bad", malformedProblem, "yolo pack lint"} {
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

// A MANIFEST THAT DOES NOT DECODE says nothing about which profiles it declares, so the refusal
// cannot name it as the declarer; it keeps the undeclared message and names the pack this launch
// could not use beside it, rather than reading as a typo. With no unusable pack the message is
// the plain one.
func TestHostLaunchNamesAnUnusablePackBesideAnUndeclaredProfile(t *testing.T) {
	for _, broken := range []bool{true, false} {
		_, dir := malformedPackHome(t, "", false, `,"use_profiles":{"claude":"bp"}`)
		if broken {
			writeFile(t, filepath.Join(dir, "pack.json"),
				`{"name":"bad","fieldFromANewerYolo":1,"contributes":[`+
					strings.TrimSuffix(badProfileContribs, ",")+`]}`)
		}
		rc, reached, errw := hostExecRun(t, "claude")
		if rc == 0 || reached {
			t.Fatalf("broken=%v: bp is declared by nothing this launch reads; rc=%d\n%s", broken, rc, errw)
		}
		if !strings.Contains(errw, `no profile named "bp"`) {
			t.Errorf("broken=%v: the undeclared message is missing:\n%s", broken, errw)
		}
		names := strings.Contains(errw, "a profile only it declares")
		if names != broken {
			t.Errorf("broken=%v: the refusal names an unusable pack = %v:\n%s", broken, names, errw)
		}
	}
}

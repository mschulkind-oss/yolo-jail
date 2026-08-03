package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requires_test.go covers the jail half of the `requires` kind: it generates NOTHING, and an
// absent required binary is reported BY NAME rather than discovered later as a tool that
// mysteriously does nothing.

// writeRequiresPack stages a one-pack tree declaring `requires` for each named bin, and
// returns the pack root to point YOLO_PACK_ROOT at.
func writeRequiresPack(t *testing.T, hints string, bins ...string) string {
	t.Helper()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "needy")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var entries []string
	for _, b := range bins {
		e := `{"kind":"requires","bin":"` + b + `"`
		if hints != "" {
			e += `,"install_hints":` + hints
		}
		entries = append(entries, e+"}")
	}
	manifest := `{"name":"needy","contributes":[` + strings.Join(entries, ",") + `]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return packRoot
}

// TestRequiresGeneratesNoLauncher is the structural half of the kind, and the reason it is
// not just "program without the install": a `requires` puts nothing on PATH, so it cannot
// shadow the very binary it is asserting. Defect 11.1 is not merely ordered away for this
// kind — there is no file to order.
func TestRequiresGeneratesNoLauncher(t *testing.T) {
	home := t.TempDir()
	packRoot := writeRequiresPack(t, "", "fzf", "fd")

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(e.LauncherDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, ent := range entries {
			names = append(names, ent.Name())
		}
		t.Errorf("a `requires` contribution generated %v — it must generate NOTHING; a "+
			"launcher for a binary yolo does not install is exactly the shadowing hazard "+
			"the kind exists to avoid", names)
	}
}

// TestAssertRequiredBinsNamesTheMissingBin: the whole reason the kind beats staying silent
// (the fzf example pack's old workaround) is that an absent dependency is REPORTED. The
// message must name the bin, the pack, and — when the pack carries them — that host hints
// exist.
func TestAssertRequiredBinsNamesTheMissingBin(t *testing.T) {
	home := t.TempDir()
	packRoot := writeRequiresPack(t, `{"brew":"fzf","apt":"fzf"}`, "definitely-not-a-real-binary")

	var warnings strings.Builder
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	e.Stderr = &warnings
	AssertRequiredBins(e)

	got := warnings.String()
	if !strings.Contains(got, "definitely-not-a-real-binary") {
		t.Errorf("the warning must NAME the missing bin:\n%s", got)
	}
	if !strings.Contains(got, "needy") {
		t.Errorf("the warning must name the pack that requires it:\n%s", got)
	}
	if !strings.Contains(got, "apt, brew") {
		t.Errorf("the warning should list the pack's host hints, sorted:\n%s", got)
	}
}

// A required binary that IS on PATH produces no output at all. A check that warns either way
// trains people to ignore it.
func TestAssertRequiredBinsSilentWhenPresent(t *testing.T) {
	home := t.TempDir()
	packRoot := writeRequiresPack(t, "", "presenttool")

	// Put the binary somewhere BootPath actually looks. $HOME/.local/bin is on it, and using
	// a real PATH entry rather than stubbing the probe is what makes this test cover the
	// "which PATH do we ask about" question — the answer must be the AGENT's PATH (BootPath),
	// not the entrypoint process's, which at this point in the boot is still the container
	// default.
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "presenttool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var warnings strings.Builder
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	e.Stderr = &warnings
	AssertRequiredBins(e)

	if got := warnings.String(); got != "" {
		t.Errorf("a present required binary must produce no output, got:\n%s", got)
	}
}

// A non-executable file of the right name does not satisfy a requirement — the assertion is
// "this command runs", not "this path exists".
func TestAssertRequiredBinsIgnoresANonExecutable(t *testing.T) {
	home := t.TempDir()
	packRoot := writeRequiresPack(t, "", "notexec")

	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "notexec"), []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var warnings strings.Builder
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	e.Stderr = &warnings
	AssertRequiredBins(e)

	if !strings.Contains(warnings.String(), "notexec") {
		t.Errorf("a non-executable file must not satisfy a requirement:\n%s", warnings.String())
	}
}

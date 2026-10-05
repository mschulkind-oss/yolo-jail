package hostfloor

// realcapture_test.go drives the REAL capture driver (capture.Run, with the full reference scan a
// container capture records) over an installer that lays out codex's standalone shape, as its vendor
// installer leaves it in a jail (MEASURED 2026-10-04 against codex 0.158.0): one directory per release
// under ~/.codex/packages/standalone/releases, `current` an ABSOLUTE link to one of them, and
// ~/.local/bin/codex an absolute link THROUGH `current` to its bin/codex. Only the program itself is
// a stand-in, a shell script that prints a version.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// codexVersionsDir is the versions directory packs/codex declares.
const codexVersionsDir = ".codex/packages/standalone/releases"

// codexProgram is packs/codex's program as the floor reads it.
func codexProgram() Program {
	return Program{Pack: "codex", Install: packdecl.Install{Kind: "native", Bin: "codex",
		InstallerURL: "https://example.invalid/codex/install.sh", VersionsDir: codexVersionsDir}}
}

// codexRelease is the release directory's name codex's installer gives version.
func codexRelease(version string) string { return version + "-x86_64-unknown-linux-musl" }

// realCodexCapture runs capture.Run against a fresh home with an installer that lays out codex
// version's standalone shape, admits the result into cs and returns it.
func realCodexCapture(t *testing.T, cs *captureStore, version string) *capture.Entry {
	t.Helper()
	home := resolvedTemp(t)
	must(t, os.MkdirAll(filepath.Join(home, ".local"), 0o755))
	must(t, os.MkdirAll(filepath.Join(home, ".codex", "packages", "standalone"), 0o755))
	staged, err := cs.store.Stage("codex-" + version)
	must(t, err)
	script := `set -e
S="$HOME/.codex/packages/standalone"
R="$S/releases/` + codexRelease(version) + `"
mkdir -p "$R/bin" "$HOME/.local/bin"
printf '#!/bin/sh\necho codex-cli ` + version + `\n' > "$R/bin/codex"
chmod 755 "$R/bin/codex"
printf '{"version":"` + version + `"}' > "$R/codex-package.json"
ln -s "$R" "$S/current"
ln -s "$S/current/bin/codex" "$HOME/.local/bin/codex"
`
	res, err := capture.Run(capture.Options{Home: home, Out: staged, Command: []string{"sh", "-c", script},
		ScanContentRefs: true})
	if err != nil {
		t.Fatalf("capture.Run: %v", err)
	}
	if !res.Manifest.Relocatable {
		t.Fatalf("the capture is not relocatable: %v", res.Manifest.NotRelocatable)
	}
	entry, err := cs.store.AdmitEntry(staged)
	must(t, err)
	cs.byBin["codex"] = entry
	return entry
}

// TestACodexCaptureIsTheFloorsCodex: codex's capture, made by the real driver, is the floor's codex.
// Its ~/.local/bin/codex reaches the program only through a link that is a DIRECTORY component of
// the path (`current`), which the manifest walk resolves as the kernel would, so it is not "no floor
// entry"; it materializes relocated out of the capture's home, its version is the release the
// versions directory holds, and it starts with no environment at all.
func TestACodexCaptureIsTheFloorsCodex(t *testing.T) {
	w := newLinuxWorld(t)
	cs := newCaptureStore(t)
	entry := realCodexCapture(t, cs, "0.158.0")
	w.floor.ResolveCapture = cs.resolve
	w.floor.Capture = func(string) error { t.Fatal("a capture ran although the store had one"); return nil }
	codex := codexProgram()
	if st := w.floor.Status(codex); st.Disposition != Missing {
		t.Fatalf("Status = %s (%s), want missing: the floor can hold codex", st.Disposition, st.Reason)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), codex)
	if err != nil || outcome != Installed {
		t.Fatalf("Ensure = %s %v\n%s", outcome, err, w.out.String())
	}
	if st.Record.Capture != entry.Key || st.Record.Version != codexRelease("0.158.0") {
		t.Errorf("record = %+v, want capture %s at %s", st.Record, entry.Key, codexRelease("0.158.0"))
	}
	cmd := exec.Command(st.Launcher, "--version")
	cmd.Env = []string{}
	if got, err := cmd.CombinedOutput(); err != nil || string(got) != "codex-cli 0.158.0\n" {
		t.Fatalf("running the floor's codex: %q %v", got, err)
	}
	current, err := os.Readlink(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(st.Record.Entry))),
		".codex", "packages", "standalone", "current"))
	if err != nil || !strings.HasPrefix(current, w.floor.Dir) {
		t.Errorf("current links to %q (%v), not into the floor: the capture was not relocated", current, err)
	}
}

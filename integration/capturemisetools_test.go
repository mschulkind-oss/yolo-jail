package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// capturemisetools_test.go is the container-level cell for FP-D19
// (docs/design/forked-programs-as-packs.md, OQ-FP10 ruled 2026-10-05): the user config's
// `mise_tools` reach no capture jail. Two `yolo capture` acts run against a user config declaring
// a mise tool no registry has, a fork's build (a sealed jail) and an installer program's capture
// (an unsealed one). Each jail's command refuses when the jail's global mise config, the file its
// `mise install` installs from, is missing or names that tool, so a capture fails if the tool
// crossed. No launch runs, so no ordinary jail meets the tool's failing install.
//
// HERMETIC as forkbuild_test.go and capture_test.go are. ⚠ THE CAPTURE STORE IS THE DEVELOPER'S
// OWN (capture_test.go says why), so each test removes only the entries it added, under bin names
// no other test uses.

const (
	miseFixtureTool   = "capturefixture-user-tool"
	miseFixtureConfig = `"$HOME/.config/mise/config.toml"`
)

// miseFixturePacks writes a user config selecting packs, a JSON list body, and declaring
// miseFixtureTool as a mise tool of the user's.
func miseFixturePacks(t *testing.T, packs string) {
	t.Helper()
	packHome(t, `{"packs": [`+packs+`], "mise_tools": {"`+miseFixtureTool+`": "1.0.0"}}`)
}

// miseFixtureCapture runs `yolo capture bin` and fails the test, with what it printed, unless it
// stored an entry.
func miseFixtureCapture(t *testing.T, bin, what string) string {
	t.Helper()
	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(store, "staging", bin)) })
	r := runYoloCLI(t, t.TempDir(), "capture", bin)
	if r.rc != 0 {
		t.Fatalf("%s failed, rc %d: its jail was handed the user's mise tool %s, or could not "+
			"read its mise config\n%s", what, r.rc, miseFixtureTool, r.combined())
	}
	return r.combined()
}

// A FORK'S SEALED BUILD is handed none of the user's mise_tools, and its launch says how many it
// withheld.
func TestAForkBuildIsHandedNoneOfTheUsersMiseTools(t *testing.T) {
	requireJail(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	const bin, basePack, forkPack = "misefixture", "misefixture-base", "misefixture-fork"
	repo, _ := forkFixtureRepo(t)
	base, fork := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(base, "install.sh"), "#!/bin/bash\nexit 1\n")
	write(filepath.Join(base, "pack.json"), `{"name":"`+basePack+`","contributes":[{"kind":"program","bin":"`+
		bin+`","via":"installer","url":"file:///ctx/packs/`+basePack+`/install.sh"}]}`)
	build := `test -f ` + miseFixtureConfig + ` && ! grep -q ` + miseFixtureTool + ` ` + miseFixtureConfig +
		` && mkdir -p "$HOME/.local/bin" && printf '#!/bin/sh\necho misefixture\n' > "$HOME/.local/bin/` + bin +
		`" && chmod +x "$HOME/.local/bin/` + bin + `"`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	write(filepath.Join(fork, "pack.json"), `{"name":"`+forkPack+`","contributes":[{"kind":"program","bin":"`+bin+
		`","via":"source","fork_of":"`+basePack+`","source":"git+file://`+repo+`?ref=main",`+
		`"build":"`+buildJSON+`","produces":[".local/bin/`+bin+`"]}]}`)
	miseFixturePacks(t, `{"source": "file://`+base+`", "name": "`+basePack+`"}, `+
		`{"source": "file://`+fork+`", "name": "`+forkPack+`"}`)

	out := miseFixtureCapture(t, bin, "the fork's build")
	if !strings.Contains(out, "Sealed build: 1 of your mise_tools is withheld") {
		t.Errorf("the fork's build does not say it withheld the user's mise tool:\n%s", out)
	}
}

// AN INSTALLER PROGRAM'S CAPTURE, which is not sealed, is handed none of the user's mise_tools
// either: FP-D19 widens OQ-FP10 to every capture jail.
func TestAnInstallerCaptureIsHandedNoneOfTheUsersMiseTools(t *testing.T) {
	requireJail(t)
	const bin, pack = "yolo-capture-misefixture", "capture-misefixture-pack"
	dir := t.TempDir()
	installer := "#!/bin/bash\nset -euo pipefail\n" +
		"test -f " + miseFixtureConfig + "\n" +
		"if grep -q " + miseFixtureTool + " " + miseFixtureConfig + "; then\n" +
		"  echo 'the capture jail was handed the user mise tool' >&2\n  exit 1\nfi\n" +
		"mkdir -p \"$HOME/.local/bin\"\n" +
		"printf '#!/bin/sh\\necho misefixture\\n' > \"$HOME/.local/bin/" + bin + "\"\n" +
		"chmod +x \"$HOME/.local/bin/" + bin + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte(installer), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"name":"`+pack+`","contributes":[`+
		`{"kind":"program","bin":"`+bin+`","via":"installer","url":"file:///ctx/packs/`+pack+`/install.sh"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	miseFixturePacks(t, `{"source": "file://`+dir+`", "name": "`+pack+`"}`)

	miseFixtureCapture(t, bin, "the installer's capture")
}

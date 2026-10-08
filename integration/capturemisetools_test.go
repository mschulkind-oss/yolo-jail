package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// capturemisetools_test.go is the container-level cell for FP-D19
// (docs/design/forked-programs-as-packs.md, OQ-FP10 ruled 2026-10-05): the user config's
// a mise tool no registry has, a fork's build (a sealed jail) and an installer's explicit capture
// (an unsealed one). Each jail's command refuses when its global mise config, the file `mise install`
// installs from, is missing or names that tool, so a capture fails if the tool crossed. These two
// handoff cases make no ordinary launch; the separate opt-in test below exercises the launch's
// installer auto-capture trigger.
//
// HERMETIC as forkbuild_test.go and capture_test.go are. Each fixture uses a private HOME-derived
// capture/pack store linked only to the run's explicitly shared children, so cleanup cannot see
// or remove another test's capture.

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
	withPrivateFixtureYoloStore(t)
	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before, bin) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(store, "staging", bin)) })
	r := runCommand(t, t.TempDir(), []string{"capture", bin}, withHostSemantics())
	if r.rc != 0 {
		t.Fatalf("%s failed, rc %d: its jail was handed the user's mise tool %s, or could not "+
			"read its mise config\n%s", what, r.rc, miseFixtureTool, r.combined())
	}
	return r.combined()
}

func TestAnInstallerAutoCaptureRunsForAChildThatOptsIn(t *testing.T) {
	requireJail(t)
	const bin, packName = "yolo-autocapture-host-context-fixture", "autocapture-host-context-fixture"
	pack := t.TempDir()
	installer := "#!/bin/bash\nset -euo pipefail\n" +
		"mkdir -p \"$HOME/.local/bin\"\n" +
		"printf '#!/bin/sh\\necho AUTOCAPTURE_TOOL_RAN\\necho AUTO_HOST_LOOPBACK=$YOLO_HOST_LOOPBACK\\n' > \"$HOME/.local/bin/" + bin + "\"\n" +
		"chmod +x \"$HOME/.local/bin/" + bin + "\"\necho AUTOCAPTURE_INSTALLER_RAN\n"
	if err := os.WriteFile(filepath.Join(pack, "install.sh"), []byte(installer), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + packName + `","contributes":[{"kind":"program","bin":"` +
		bin + `","via":"installer","url":"file:///ctx/packs/` + packName + `/install.sh"}]}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+packName+`"}]}`)
	withPrivateFixtureYoloStore(t)
	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before, bin) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(store, "staging", bin)) })

	workspace := writeProject(t, `{}`)
	recordPodman, podmanLog := podmanRunArgumentRecorder(t)
	r := runCommand(t, workspace, append(jailRunArgs(), "--", bin),
		withEnv(paths.NoProgramReadinessEnv+"=1", "YOLO_NO_AUTO_CAPTURE="), withHostSemantics(), recordPodman)
	if r.rc != 0 {
		t.Fatalf("the opt-in auto-capture launch failed: rc %d\n%s", r.rc, r.combined())
	}
	assertPhysicalNestedPodmanControls(t, workspace, r.combined(), podmanLog)
	for _, want := range []string{"auto-capture", "AUTOCAPTURE_INSTALLER_RAN", "AUTOCAPTURE_TOOL_RAN"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the opt-in installer auto-capture launch lacks %q:\n%s", want, r.combined())
		}
	}
	added := newCaptureEntries(t, store, before, bin)
	if len(added) != 1 {
		t.Fatalf("the opt-in launch recorded %d installer captures, want exactly one: %v", len(added), added)
	}
	entry, err := (&capture.Store{Dir: store}).Resolve(added[0])
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := entrypoint.ReadCaptureReceipts(capture.ReceiptsPath(entry.Root))
	if err != nil || len(receipts) != 1 || receipts[0].Bin != bin || receipts[0].Act != entrypoint.ReceiptActRecord {
		t.Fatalf("the auto-capture did not write one installer record receipt: %+v (%v)", receipts, err)
	}
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

package cli

// forkdecline_test.go pins the host verbs that meet a FORK's program before this build delivers
// one (docs/design/forked-programs-as-packs.md): each says, by name, that the program is built
// from a fork's source, and none of them runs the base's upstream delivery in its place.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// forkManifest forks base's bin program from a fixture source.
func forkManifest(name, base, bin string) string {
	return `{"name":"` + name + `","contributes":[{"kind":"program","bin":"` + bin + `","via":"source",` +
		`"fork_of":"` + base + `","source":"git+https://example.invalid/` + bin + `-fork?ref=main",` +
		`"build":"make install","produces":[".local/bin/` + bin + `"]}]}`
}

// forkHostFixture is a temp HOME whose user config selects a base pack declaring program with
// the given contribution and a fork pack forking its bin. It returns the home.
func forkHostFixture(t *testing.T, bin, baseProgram string) string {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(floortest.ResolvedTemp(t))
	packs := floortest.ResolvedTemp(t)
	writeFile(t, filepath.Join(packs, "basepack", "pack.json"),
		`{"name":"basepack","contributes":[`+baseProgram+`]}`)
	writeFile(t, filepath.Join(packs, "forkpack", "pack.json"), forkManifest("forkpack", "basepack", bin))
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+filepath.Join(packs, "forkpack")+`","name":"forkpack"}]}`)
	return home
}

// `yolo host -- <forked bin>`: the floor holds no source-built program yet, and the no-copy line
// says it is the fork's, rather than installing the base's npm package into the floor.
func TestHostLaunchOfAForkedProgramSaysTheFloorHoldsNoneYet(t *testing.T) {
	forkHostFixture(t, "floorcli", `{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}`)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	dist := withTestFloor(t)
	dist.Publish("floorcli-pkg", "1.0.0", "bin=floorcli")
	stub := filepath.Join(stubBins(t, "floorcli"), "floorcli")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || got.target != stub {
		t.Fatalf("rc=%d target=%s, want the PATH copy %s\n%s", rc, got.target, stub, errw.String())
	}
	for _, want := range []string{"yolo has no copy of floorcli built from its fork's source yet",
		"built from source by fork pack forkpack"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errw.String())
		}
	}
	if calls := dist.NpmCalls("install"); len(calls) != 0 {
		t.Errorf("the floor installed the base's npm package for a forked program: %v", calls)
	}
}

// `yolo capture <forked bin>` of a fork with no pin: refused, naming the command that pins it —
// never filed with the npm programs ("names a registry version") and never run as the base's
// installer.
func TestCaptureOfAnUnpinnedForkNamesThePin(t *testing.T) {
	forkHostFixture(t, "probetool", captureFixtureInstaller)
	withFakeCaptureJail(t, func(run.Options) int {
		t.Error("a capture jail ran for a fork with no pin")
		return 1
	})
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc == 0 {
		t.Fatal("capture of an unpinned fork succeeded")
	}
	if !strings.Contains(errw.String(), "it has no pin yet — run `yolo pack install`") {
		t.Errorf("the refusal does not name the pin:\n%s", errw.String())
	}
	if strings.Contains(errw.String(), "registry version") {
		t.Errorf("a forked program was described as an npm one:\n%s", errw.String())
	}
}

// `yolo pack update` in a jail: a forked program has no update mode — its pin moves it — so its
// launcher is not run, and the line says what does move it.
func TestPackUpdateDoesNotRefreshAForkedProgram(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "basepack", "pack.json"),
		`{"name":"basepack","contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`)
	writeFile(t, filepath.Join(root, "forkpack", "pack.json"), forkManifest("forkpack", "basepack", "tool"))
	e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": root})
	if err := os.MkdirAll(e.LaunchDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(e.LaunchDir(), "tool"), "#!/bin/sh\nexit 0\n")
	// refreshPrograms reads the tree through entrypoint.LoadJailPacks, which switches the process
	// to the tolerant manifest decoder; every later test in this binary must stay strict.
	t.Cleanup(packload.OverrideSkewTolerance(false))
	var ran []string
	var out, errw bytes.Buffer
	rc := refreshPrograms(e, richtext.Printer{W: &out}, &errw,
		func(bin, path string) error { ran = append(ran, bin); return nil })
	if rc != 0 || len(ran) != 0 {
		t.Fatalf("rc=%d ran=%v, want no launcher run for a forked program\n%s", rc, ran, errw.String())
	}
	if !strings.Contains(out.String(), "built from source by fork pack forkpack") {
		t.Errorf("the refresh does not say why the fork was left alone:\n%s", out.String())
	}
}

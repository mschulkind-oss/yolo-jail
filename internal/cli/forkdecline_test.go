package cli

// forkdecline_test.go pins the host verbs that meet a FORK's program they cannot deliver
// (docs/design/forked-programs-as-packs.md): each says, by name, that the program is built from a
// fork's source, and none of them runs the base's upstream delivery in its place. The host floor's
// delivery of a pinned fork is hostfloorfork_test.go's.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// forkManifest forks base's bin program from a fixture source: a LOCAL repository that does not
// exist, so a verb that pins the fork (FP-D18) fails at once instead of reaching the network.
func forkManifest(name, base, bin string) string {
	return `{"name":"` + name + `","contributes":[{"kind":"program","bin":"` + bin + `","via":"source",` +
		`"fork_of":"` + base + `","source":"git+file:///nonexistent/yolo-test/` + bin + `-fork?ref=main",` +
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

// `yolo host -- <forked bin>` of a fork with no pin THAT IT CANNOT PIN (FP-D18: its source cannot be
// fetched): the floor has no build to ask for, so the no-copy line names the fork, why the pin
// failed and the next step, and the launch refuses (HNR-D2) — never installing the base's
// npm package into the floor in the fork's place.
func TestHostLaunchOfAForkItCannotPinNamesWhyAndRefuses(t *testing.T) {
	forkHostFixture(t, "floorcli", `{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}`)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	dist := withLinuxTestFloor(t) // where the floor holds a fork's build at all: the pin is what is missing
	dist.Publish("floorcli-pkg", "1.0.0", "bin=floorcli")
	stubContainerRuntime(t) // and a machine that can build: else the missing runtime is what it says
	stub := filepath.Join(stubBins(t, "floorcli"), "floorcli")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 127 || got.execed {
		t.Fatalf("rc=%d target=%s, want 127 and no exec, never the PATH copy %s (HNR-D2)\n%s", rc, got.target, stub,
			errw.String())
	}
	for _, want := range []string{"yolo has no copy of floorcli on this machine", declaredNoCopyRefusal,
		"built from source by fork pack forkpack", "it has no pin, and pinning it failed",
		"fix what that names and launch again"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errw.String())
		}
	}
	if calls := dist.NpmCalls("install"); len(calls) != 0 {
		t.Errorf("the floor installed the base's npm package for a forked program: %v", calls)
	}
}

// `yolo host -- <forked bin>` of a fork with no pin ON A MACHINE THAT CANNOT BUILD (no container
// runtime on PATH, as on the macOS CI runner): the floor does not pin it — a pin fetches the fork's
// source, and nothing could build what it named — so the no-copy line names the missing runtime
// and the step that ends it (the next launch pins and builds the fork itself, FP-D18), and the
// launch refuses (HNR-D2), installing nothing of the base's.
func TestHostLaunchOfAnUnpinnedForkOnAMachineThatCannotBuildNamesTheRuntimeAndDoesNotPin(t *testing.T) {
	forkHostFixture(t, "floorcli", `{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}`)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	dist := withLinuxTestFloor(t)
	dist.Publish("floorcli-pkg", "1.0.0", "bin=floorcli")
	pins := 0
	inner := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := inner(out, progs)
		pin := f.PinFork
		f.PinFork = func(p hostfloor.Program, say func(string)) (string, string) {
			pins++
			return pin(p, say)
		}
		return f
	}
	t.Cleanup(func() { newHostFloor = inner })
	stubDir := stubBins(t, "floorcli")
	withoutContainerRuntime(t, stubDir)
	got := captureHostExec(t)
	var errw bytes.Buffer
	stub := filepath.Join(stubDir, "floorcli")
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 127 || got.execed {
		t.Fatalf("rc=%d target=%s, want 127 and no exec, never the PATH copy %s (HNR-D2)\n%s", rc, got.target, stub,
			errw.String())
	}
	for _, want := range []string{"yolo has no copy of floorcli on this machine", declaredNoCopyRefusal,
		"built from source by fork pack forkpack, which has no pin yet",
		"no container runtime (podman) is on PATH",
		"install one (`yolo check` names how on this machine) and the next `yolo host` launch pins the fork and builds it"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errw.String())
		}
	}
	if pins != 0 || strings.Contains(errw.String(), "pinning it failed") {
		t.Errorf("the floor tried to pin a fork it could not build (%d pins):\n%s", pins, errw.String())
	}
	if _, err := os.Stat(forkLockPath()); !os.IsNotExist(err) {
		t.Errorf("a launch that could not build wrote the fork lock: %v", err)
	}
	if calls := dist.NpmCalls("install"); len(calls) != 0 {
		t.Errorf("the floor installed the base's npm package for a forked program: %v", calls)
	}
}

// `yolo capture <forked bin>` of a fork with no pin that it cannot pin: refused, naming why and the
// next step — never filed with the npm programs ("names a registry version") and never run as the
// base's installer.
func TestCaptureOfAForkItCannotPinNamesWhy(t *testing.T) {
	forkHostFixture(t, "probetool", captureFixtureInstaller)
	withFakeCaptureJail(t, func(run.Options) int {
		t.Error("a capture jail ran for a fork with no pin")
		return 1
	})
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc == 0 {
		t.Fatal("capture of an unpinned fork succeeded")
	}
	if !strings.Contains(errw.String(), "it has no pin, and pinning it failed") {
		t.Errorf("the refusal does not say why the fork has no pin:\n%s", errw.String())
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

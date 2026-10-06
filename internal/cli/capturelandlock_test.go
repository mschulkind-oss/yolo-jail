package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// capturelandlock_test.go pins the capture act's two new arms at their call sites
// (docs/design/host-tool-provisioning.md HP-D2, HP-D18): the floor on a Mac capturing through the
// macos-user act, under that runtime and through no environment variable; the floor on Linux with no
// runtime capturing on the host, where the kernel can confine it; `yolo capture`'s own choice of arm;
// and the recorded origin that keeps a host capture out of every jail. The confined run itself is
// capturelandlock_linux_test.go's, and the confinement internal/capture's.

// errHostCaptureDisarmed is the test binary's default answer to "can this kernel confine a host
// capture?" (init, below).
var errHostCaptureDisarmed = errors.New("test guard: the host capture is disarmed; a test that drives it " +
	"calls withHostConfinement")

func init() {
	// THE HOST CAPTURE AND A MAC'S PROBES ARE DISARMED for every test of this package, as the floor's
	// acts are (disarmTheHostFloor): a capture test on a Linux machine with no podman would otherwise
	// take the host capture and run a fixture's installer for real, and a floor test would ask this
	// machine whether it is a Mac with a sandbox account. A test that is about either sets it.
	hostConfinementABI = func() (int, error) { return 0, errHostCaptureDisarmed }
	hostFloorMacProbes = func() macCaptureProbes { return macSetup{}.probes() }
}

// withHostConfinement makes this machine's kernel one that confines a host capture at abi, or, with
// err, one that cannot.
func withHostConfinement(t *testing.T, abi int, err error) {
	t.Helper()
	orig := hostConfinementABI
	hostConfinementABI = func() (int, error) { return abi, err }
	t.Cleanup(func() { hostConfinementABI = orig })
}

// onCaptureGOOS makes goos the platform `yolo capture` chooses its arm for (captureHostGOOS).
func onCaptureGOOS(t *testing.T, goos string) {
	t.Helper()
	orig := captureHostGOOS
	captureHostGOOS = goos
	t.Cleanup(func() { captureHostGOOS = orig })
}

// macSetup is a Mac as the floor's capture predicate reads it. The zero value is a Mac before
// `yolo macos-setup`: not root, Seatbelt present, no sandbox account, no terminal, no sudo credentials.
type macSetup struct {
	root, noSeatbelt, account, terminal, sudo bool
}

func (m macSetup) probes() macCaptureProbes {
	return macCaptureProbes{
		Geteuid: func() int {
			if m.root {
				return 0
			}
			return 501
		},
		Which:               func(name string) bool { return name == "sandbox-exec" && !m.noSeatbelt },
		SandboxUserExists:   func() bool { return m.account },
		StdinIsTerminal:     func() bool { return m.terminal },
		SudoWithoutPassword: func() bool { return m.sudo },
	}
}

func withMac(t *testing.T, m macSetup) {
	t.Helper()
	orig := hostFloorMacProbes
	hostFloorMacProbes = m.probes
	t.Cleanup(func() { hostFloorMacProbes = orig })
}

// noRuntimeHere makes this test's machine one with no container runtime selected and none on PATH.
func noRuntimeHere(t *testing.T) {
	t.Helper()
	t.Setenv("YOLO_RUNTIME", "")
	t.Setenv("PATH", floortest.ResolvedTemp(t))
}

// THE MAC'S FLOOR CAPTURES THROUGH THE MACOS-USER ACT (HP-D2), at its call site: a Mac with the
// sandbox account set up, no capture of nativecli, and a terminal: `yolo host -- nativecli` runs the
// capture act once under the macos-user runtime — handed to the act, never set in the environment the
// agent is exec'd with — and then execs the floor's copy of what it captured. The container act is
// never run: on a Mac it would record a Linux entry, which this floor cannot run. Drop the runtime
// from the Mac's Capture wiring and the act this test stands in is never reached.
func TestAMacsFloorCapturesThroughTheMacosUserAct(t *testing.T) {
	nativeFloorFixture(t)
	// The PRODUCTION floor, its own Capture wiring included, on a Mac (the fixture's test floor guards
	// its capture act); only the Node download is the test's, and it is never asked for.
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := productionHostFloor(out, progs)
		f.GOOS, f.GOARCH = "darwin", "arm64"
		f.Node.BaseURL = "http://127.0.0.1:1/test-guard-no-node-download"
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	withMac(t, macSetup{account: true, terminal: true})
	t.Setenv("YOLO_RUNTIME", "")
	origAct, origOn := hostFloorCaptureAct, hostFloorCaptureActOn
	hostFloorCaptureAct = func(args []string, _, _ io.Writer, _ bool) int {
		t.Errorf("the Mac's floor ran the container capture act for %q", args)
		return 1
	}
	var acts []string
	hostFloorCaptureActOn = func(rt string, args []string, _, _ io.Writer, _ bool) int {
		acts = append(acts, rt+" "+strings.Join(args, " "))
		admitRelocatableCapture(t, args[0], "#!/bin/sh\necho nativecli from the Mac's capture \"$@\"\n")
		return 0
	}
	t.Cleanup(func() { hostFloorCaptureAct, hostFloorCaptureActOn = origAct, origOn })

	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"nativecli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	if strings.Join(acts, "|") != "macos-user nativecli" {
		t.Fatalf("the capture act ran %q, want once for nativecli under macos-user\n%s", acts, errw.String())
	}
	if launcher := filepath.Join(paths.HostFloorDir(), "bin", "nativecli"); got.target != launcher {
		t.Fatalf("exec'd %s, want the floor's copy %s\n%s", got.target, launcher, errw.String())
	}
	if want := "running `yolo capture nativecli` (the macos-user sandbox account runs its installer once"; !strings.Contains(errw.String(), want) {
		t.Errorf("stderr lacks %q:\n%s", want, errw.String())
	}
	if v := envValue(got.env, "YOLO_RUNTIME"); v != "" || os.Getenv("YOLO_RUNTIME") != "" {
		t.Errorf("YOLO_RUNTIME reached the agent's environment (%q) or this process's (%q)", v, os.Getenv("YOLO_RUNTIME"))
	}
}

// THE ACT HANDS ITS RUNTIME TO THE RUN PIPELINE ALONE: the Mac floor's act (hostFloorCaptureActOn's
// production value) runs `yolo capture` with the pipeline's Getenv answering macos-user for
// YOLO_RUNTIME and this process's environment for everything else, and leaves the process environment
// as it was; a plain `yolo capture` leaves the pipeline its own resolution.
func TestTheMacosUserCaptureActHandsItsRuntimeToTheRunPipelineAlone(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	t.Setenv("YOLO_RUNTIME", "")
	entries := []capture.ManifestEntry{
		{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 16},
	}
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, entries))
	var out, errw bytes.Buffer
	if rc := hostFloorCaptureActOn("macos-user", []string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	if seen.Getenv == nil || seen.Getenv("YOLO_RUNTIME") != "macos-user" {
		t.Fatalf("the pipeline was not handed the macos-user runtime (Getenv set: %v)", seen.Getenv != nil)
	}
	if seen.Getenv("HOME") != os.Getenv("HOME") {
		t.Errorf("the pipeline's Getenv answers HOME %q, not this process's %q", seen.Getenv("HOME"), os.Getenv("HOME"))
	}
	if os.Getenv("YOLO_RUNTIME") != "" {
		t.Errorf("the act set YOLO_RUNTIME=%q in this process, which the next exec would inherit", os.Getenv("YOLO_RUNTIME"))
	}
	seen = run.Options{}
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("plain capture: rc=%d\n%s", rc, errw.String())
	}
	if seen.Getenv != nil && seen.Getenv("YOLO_RUNTIME") != "" {
		t.Errorf("a plain `yolo capture` named runtime %q to the pipeline", seen.Getenv("YOLO_RUNTIME"))
	}
}

// EACH REASON A MAC CANNOT CAPTURE, with the step that ends it, as the floor reports it: no floor
// entry, so the launch runs the PATH copy (OQ-HE11) instead of failing an install; and with the act
// able to run — the account set up, and a terminal for sudo or sudo needing none — the entry is the
// floor's to install.
func TestAMacsFloorNamesWhyTheMacosUserActCannotRun(t *testing.T) {
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	claude := hostfloor.Program{Pack: "claude", Install: packdecl.Install{Kind: "native", Bin: "claude",
		InstallerURL: "https://example.invalid/install.sh"}}
	for _, c := range []struct {
		name string
		mac  macSetup
		want string // "" for an installable entry
	}{
		{"before macos-setup", macSetup{terminal: true},
			"the sandbox account _yolojail, which a capture on a Mac runs its installer as, does not exist — run the " +
				"one-time setup, `yolo macos-setup`, and the next `yolo host` launch captures it"},
		{"as root", macSetup{root: true, account: true, terminal: true},
			"run `yolo host` without sudo, and it captures it"},
		{"no Seatbelt", macSetup{noSeatbelt: true, account: true, terminal: true},
			"sandbox-exec (Apple Seatbelt), which confines a capture's installer on a Mac, is not on PATH"},
		{"no terminal for sudo", macSetup{account: true},
			"run `YOLO_RUNTIME=macos-user yolo capture claude` once in a terminal, and every later `yolo host` launch " +
				"uses its result"},
		{"a terminal", macSetup{account: true, terminal: true}, ""},
		{"no terminal, sudo needing none", macSetup{account: true, sudo: true}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			withMac(t, c.mac)
			f := productionHostFloor(io.Discard, []hostfloor.Program{claude})
			f.GOOS, f.GOARCH = "darwin", "arm64"
			st := f.Status(claude)
			if c.want == "" {
				if st.Disposition != hostfloor.Missing {
					t.Fatalf("Status = %s (%s), want missing: the act can run", st.Disposition, st.Reason)
				}
				return
			}
			if st.Disposition != hostfloor.NoEntry || !strings.Contains(st.Reason, c.want) {
				t.Fatalf("Status = %s\n  %s\nwant no floor entry naming\n  %s", st.Disposition, st.Reason, c.want)
			}
			if !strings.HasPrefix(st.Reason, "there is no capture of claude on this machine, and ") ||
				strings.Contains(st.Reason, "install one") {
				t.Errorf("the reason is not the act's own clause, or carries a runtime's step: %s", st.Reason)
			}
		})
	}
}

// THE LINUX FLOOR WITH NO RUNTIME, through newHostFloor's own wiring: with a kernel that confines a
// host capture the installer agent is the floor's to install, its capture line saying how it runs;
// with one that does not, no floor entry naming both ways on (a runtime, or a kernel with Landlock);
// and a runtime the user selected is the reason when it is missing, whatever the kernel offers.
func TestALinuxFloorWithNoRuntimeCapturesWhereTheKernelConfinesIt(t *testing.T) {
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	noRuntimeHere(t)
	claude := hostfloor.Program{Pack: "claude", Install: packdecl.Install{Kind: "native", Bin: "claude",
		InstallerURL: "https://example.invalid/install.sh"}}
	floor := func() *hostfloor.Floor {
		f := productionHostFloor(io.Discard, []hostfloor.Program{claude})
		f.GOOS = "linux"
		return f
	}

	withHostConfinement(t, 6, nil)
	f := floor()
	if st := f.Status(claude); st.Disposition != hostfloor.Missing {
		t.Fatalf("with Landlock: Status = %s (%s), want missing — the host capture can make it", st.Disposition, st.Reason)
	}
	if how := f.CaptureHow(); !strings.Contains(how, "confined by Landlock") {
		t.Errorf("the capture line's parenthetical is %q, not the host capture's", how)
	}

	withHostConfinement(t, 0, errors.New("this kernel offers no Landlock (ENOSYS)"))
	want := "there is no capture of claude on this machine, and no container runtime (podman) is on PATH to run " +
		"`yolo capture` with, and yolo cannot confine its installer on this host instead (this kernel offers no " +
		"Landlock (ENOSYS)) — install podman (`yolo check` names how on this machine), or run a kernel with " +
		"Landlock enabled, and the next `yolo host` launch captures it"
	if st := floor().Status(claude); st.Disposition != hostfloor.NoEntry || st.Reason != want {
		t.Fatalf("without Landlock: Status = %s\n  %s\nwant\n  %s", st.Disposition, st.Reason, want)
	}

	withHostConfinement(t, 6, nil)
	t.Setenv("YOLO_RUNTIME", "podman")
	want = "there is no capture of claude on this machine, and no container runtime (podman) is on PATH to run " +
		"`yolo capture` with — install one (`yolo check` names how on this machine) and the next `yolo host` " +
		"launch captures it"
	if st := floor().Status(claude); st.Disposition != hostfloor.NoEntry || st.Reason != want {
		t.Fatalf("with podman selected and missing: Status = %s\n  %s\nwant\n  %s", st.Disposition, st.Reason, want)
	}
}

// `yolo capture`'S ARM IS CHOSEN AT ITS CALL SITE (captureHostWith): no runtime and a kernel that
// confines it → the host capture, never the run pipeline, its receipt carrying the host's origin; a
// runtime on PATH, or one the user selected, → the run pipeline, never the host capture; no runtime
// and no Landlock → the pipeline's own refusal, after a line naming both ways on. Those are the LINUX
// arms, pinned through captureHostGOOS so a Mac runs them too: Landlock is Linux's, and a Mac with
// no runtime takes the pipeline whatever its kernel answers (the last subtest).
func TestTheCaptureActChoosesItsArmAtTheCallSite(t *testing.T) {
	entries := []capture.ManifestEntry{
		{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 16},
	}
	type ran struct{ host, jail int }
	setup := func(t *testing.T, goos string) *ran {
		onCaptureGOOS(t, goos)
		captureFixtureHome(t, captureFixtureInstaller)
		noRuntimeHere(t)
		r := &ran{}
		var seen run.Options
		jail := fakeCaptureJail(t, &seen, entries)
		withFakeCaptureJail(t, func(o run.Options) int { r.jail++; return jail(o) })
		orig := runHostCapture
		runHostCapture = func(staging string, target *captureTarget, abi int, _, _ io.Writer) int {
			r.host++
			if target.Bin != "probetool" || target.Install.InstallerURL == "" || abi != 6 {
				t.Errorf("the host capture was handed %+v at ABI %d", target, abi)
			}
			return jail(run.Options{Workspace: staging})
		}
		t.Cleanup(func() { runHostCapture = orig })
		return r
	}
	t.Run("no runtime, Landlock", func(t *testing.T) {
		r := setup(t, "linux")
		withHostConfinement(t, 6, nil)
		var out, errw bytes.Buffer
		if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 || r.host != 1 || r.jail != 0 {
			t.Fatalf("rc=%d host=%d jail=%d, want the host capture alone\n%s\n%s", rc, r.host, r.jail, out.String(),
				errw.String())
		}
		if !strings.Contains(out.String(), "this host, its installer confined by Landlock (ABI 6)") {
			t.Errorf("the capture does not say where it runs:\n%s", out.String())
		}
		store := &capture.Store{Dir: paths.CapturesDir()}
		e, rec, err := resolveFloorCapture(store, "probetool", "linux/arm64")
		if err != nil || rec.Platform != "linux/arm64+host" {
			t.Fatalf("the floor's lookup = %v %+v, want the capture under the host's origin", err, rec)
		}
		if _, _, err := resolveCaptureFor(store, "probetool", "linux/arm64"); err == nil {
			t.Errorf("a jail's lookup selected the host capture %s", e.Key)
		}
	})
	t.Run("a runtime on PATH", func(t *testing.T) {
		r := setup(t, "linux")
		withHostConfinement(t, 6, nil)
		stubBins(t, "podman")
		var out, errw bytes.Buffer
		if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 || r.host != 0 || r.jail != 1 {
			t.Fatalf("rc=%d host=%d jail=%d, want the capture jail alone\n%s", rc, r.host, r.jail, errw.String())
		}
	})
	t.Run("a runtime selected and missing", func(t *testing.T) {
		r := setup(t, "linux")
		withHostConfinement(t, 6, nil)
		t.Setenv("YOLO_RUNTIME", "podman")
		var out, errw bytes.Buffer
		captureHost([]string{"probetool"}, &out, &errw, false)
		if r.host != 0 || r.jail != 1 {
			t.Fatalf("host=%d jail=%d, want the pipeline, which refuses a selected runtime that is missing", r.host, r.jail)
		}
	})
	t.Run("no runtime, no Landlock", func(t *testing.T) {
		r := setup(t, "linux")
		withHostConfinement(t, 0, errors.New("this kernel offers no Landlock (EOPNOTSUPP)"))
		var out, errw bytes.Buffer
		captureHost([]string{"probetool"}, &out, &errw, false)
		if r.host != 0 || r.jail != 1 {
			t.Fatalf("host=%d jail=%d, want the pipeline's own refusal", r.host, r.jail)
		}
		for _, want := range []string{"no container runtime (podman) is on PATH", "this kernel offers no Landlock (EOPNOTSUPP)",
			"Install podman (`yolo check` names how on this machine), or run a kernel with Landlock enabled"} {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("stderr lacks %q:\n%s", want, errw.String())
			}
		}
	})
	t.Run("a Mac with no runtime", func(t *testing.T) {
		r := setup(t, "darwin")
		withHostConfinement(t, 6, nil)
		var out, errw bytes.Buffer
		captureHost([]string{"probetool"}, &out, &errw, false)
		if r.host != 0 || r.jail != 1 {
			t.Fatalf("host=%d jail=%d, want the pipeline: a Mac has no host capture\n%s", r.host, r.jail, errw.String())
		}
		if strings.Contains(out.String()+errw.String(), "Landlock") {
			t.Errorf("a Mac's capture names Landlock, which it never runs under:\n%s\n%s", out.String(), errw.String())
		}
	})
	t.Run("a fork's program", func(t *testing.T) {
		onCaptureGOOS(t, "linux")
		forkBuildHome(t)
		noRuntimeHere(t)
		withHostConfinement(t, 6, nil)
		builds := 0
		var seen run.Options
		build := fakeBuildJail(t, &seen, probetoolBuilt)
		withFakeCaptureJail(t, func(o run.Options) int { builds++; return build(o) })
		orig := runHostCapture
		runHostCapture = func(string, *captureTarget, int, io.Writer, io.Writer) int {
			t.Error("a fork's build ran as a host capture")
			return 1
		}
		t.Cleanup(func() { runHostCapture = orig })
		var out, errw bytes.Buffer
		captureHost([]string{"probetool"}, &out, &errw, false)
		if builds != 1 || !seen.Sealed {
			t.Fatalf("builds=%d sealed=%v, want the sealed build jail: a pack may not build on the host\n%s", builds, seen.Sealed, errw.String())
		}
	})
}

// admitCaptureAs admits a capture of bin whose record receipt names platform, stamped at, and returns
// its entry.
func admitCaptureAs(t *testing.T, store *capture.Store, bin, body, platform string, at time.Time) *capture.Entry {
	t.Helper()
	staged, err := store.Stage(bin + "-" + strings.ReplaceAll(platform, "/", "-"))
	if err != nil {
		t.Fatal(err)
	}
	tree := capture.TreeDir(staged)
	writeFile(t, filepath.Join(tree, ".local", "bin", bin), body)
	if err := os.Chmod(filepath.Join(tree, ".local", "bin", bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := capture.WriteManifest(staged, &capture.Manifest{
		Schema: capture.ManifestSchema, Home: "/home/agent", Platform: capture.Platform(),
		Surfaces: []string{".local"}, Excluded: []string{},
		Entries: []capture.ManifestEntry{
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/" + bin, Kind: capture.KindFile, Mode: "0755", Size: int64(len(body))},
		},
		AbsoluteRefs: []capture.AbsoluteRef{}, RefScan: capture.RefScanFull, Relocatable: true,
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	line := entrypoint.CaptureReceipt{Bin: bin, Declared: "https://example.invalid/i.sh", Key: entry.Key,
		Digest: capture.DigestHash(entry.Digest), Bytes: int64(len(body)), Path: entry.Root, Platform: platform,
		Act: entrypoint.ReceiptActRecord, Time: at}.Line()
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), line); err != nil {
		t.Fatal(err)
	}
	return entry
}

// A JAIL NEVER SELECTS A HOST CAPTURE, and the floor selects either: with only a host capture in the
// store a jail's launcher misses (and downloads, as with an empty store), while the floor finds it;
// with both, the floor takes the newer whichever origin it has, and the jail its own.
func TestAJailNeverSelectsAHostCapture(t *testing.T) {
	store := &capture.Store{Dir: filepath.Join(floortest.ResolvedTemp(t), "captures")}
	plat := capture.Platform()
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	host := admitCaptureAs(t, store, "tool", "#!/bin/sh\necho host\n", hostCapturePlatform(plat), t0)
	if _, _, err := resolveCaptureFor(store, "tool", plat); err == nil {
		t.Fatal("a jail's lookup selected a host capture")
	}
	var errw bytes.Buffer
	if rc := materializeCapture(materializeArgs{store: store.Dir, bin: "tool", home: floortest.ResolvedTemp(t)},
		&errw); rc == 0 {
		t.Fatalf("the in-jail materialize put a host capture into a jail's home\n%s", errw.String())
	}
	if e, _, err := resolveFloorCapture(store, "tool", plat); err != nil || e.Key != host.Key {
		t.Fatalf("the floor's lookup = %v %v, want the host capture", e, err)
	}
	jail := admitCaptureAs(t, store, "tool", "#!/bin/sh\necho jail\n", plat, t0.Add(time.Hour))
	if e, _, err := resolveFloorCapture(store, "tool", plat); err != nil || e.Key != jail.Key {
		t.Errorf("the floor's lookup = %v %v, want the newer jail capture", e, err)
	}
	if e, _, err := resolveCaptureFor(store, "tool", plat); err != nil || e.Key != jail.Key {
		t.Errorf("a jail's lookup = %v %v, want its own capture", e, err)
	}
	newer := admitCaptureAs(t, store, "tool", "#!/bin/sh\necho host again\n", hostCapturePlatform(plat), t0.Add(2*time.Hour))
	if e, _, err := resolveFloorCapture(store, "tool", plat); err != nil || e.Key != newer.Key {
		t.Errorf("the floor's lookup = %v %v, want the newest, the host's", e, err)
	}
	if e, _, err := resolveCaptureFor(store, "tool", plat); err != nil || e.Key != jail.Key {
		t.Errorf("a jail's lookup = %v %v, want its own capture still", e, err)
	}
}

// selectMacosUser makes macos-user this test's selected runtime the way a user selects one: in
// YOLO_RUNTIME, or as the user config's `runtime`. No container runtime is on PATH either way.
func selectMacosUser(t *testing.T, via string) {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	noRuntimeHere(t)
	switch via {
	case "YOLO_RUNTIME":
		t.Setenv("YOLO_RUNTIME", "macos-user")
	case "config":
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"runtime":"macos-user"}`)
	default:
		t.Fatalf("no way to select a runtime called %q", via)
	}
}

// MACOS-USER IS NO PROGRAM MISSING FROM PATH (the production floor's CaptureUnavailable, a fork's build
// predicate): with macos-user selected, in YOLO_RUNTIME or the user config's `runtime`, the floor says
// that runtime boots no container for a build to run in. It used to look "macos-user" up on PATH and
// answer that no container runtime (macos-user) was on it — a program that does not exist, named as
// the thing to install.
func TestAFloorUnderTheMacosUserRuntimeSaysItBootsNoContainer(t *testing.T) {
	for _, via := range []string{"YOLO_RUNTIME", "config"} {
		t.Run(via, func(t *testing.T) {
			selectMacosUser(t, via)
			f := productionHostFloor(io.Discard, nil)
			got := f.CaptureUnavailable()
			if strings.Contains(got, "(macos-user) is on PATH") {
				t.Fatalf("the floor looked macos-user up on PATH: %q", got)
			}
			if want := "the runtime selected here, macos-user, boots no container to run a capture jail in"; got != want {
				t.Errorf("CaptureUnavailable() = %q, want %q", got, want)
			}
		})
	}
}

// UNDER THE MACOS-USER RUNTIME THE CAPTURE ACT TAKES ITS JAIL ARM, nothing blocked: the run pipeline
// is what checks that runtime (the sandbox account, Seatbelt), so `yolo capture` neither refuses it as
// a program missing from PATH nor runs the installer on the host instead, on a Mac or on Linux.
func TestTheCaptureActTakesItsJailArmUnderTheMacosUserRuntime(t *testing.T) {
	for _, via := range []string{"YOLO_RUNTIME", "config"} {
		t.Run(via, func(t *testing.T) {
			selectMacosUser(t, via)
			withHostConfinement(t, 6, nil)
			for _, goos := range []string{"darwin", "linux"} {
				if arm := chooseCaptureArm(goos, ""); arm.blocked != "" || arm.host() || arm.hostWhy != "" {
					t.Errorf("%s: chooseCaptureArm = %+v, want the jail arm with nothing blocked", goos, arm)
				}
			}
			if got := runtimeAbsent("macos-user"); got != "" {
				t.Errorf("runtimeAbsent(macos-user) = %q, want none: it is no program to find", got)
			}
		})
	}
}

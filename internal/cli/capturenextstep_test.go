package cli

// capturenextstep_test.go pins that every way `yolo capture` stops names its next step on the line
// after it (docs/reference/happy-path-principle.md, rule 1): a command that leads back to the
// capture, or exact instructions where no command exists. Each stop printed its error alone —
// `yolo capture: the capture jail exited 7 — nothing was stored`, and then nothing.
//
// Each case drives captureHost, the command itself, so a step deleted at its call site fails here.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// stepAfter is the line after the last stderr line that starts with lead, and fails the test when
// there is none.
func stepAfter(t *testing.T, stderr, lead string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], lead) {
			if i+1 >= len(lines) {
				t.Fatalf("nothing follows %q — the stop names no next step:\n%s", lines[i], stderr)
			}
			return lines[i+1]
		}
	}
	t.Fatalf("no line starts with %q:\n%s", lead, stderr)
	return ""
}

// runCapture runs `yolo capture <bin>` and returns its rc and stderr.
func runCaptureFor(t *testing.T, bin string) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := captureHost([]string{bin}, &out, &errw, false)
	return rc, errw.String()
}

func TestEveryCaptureStopNamesItsNextStep(t *testing.T) {
	noJail := func(t *testing.T) {
		withFakeCaptureJail(t, func(run.Options) int { t.Error("no jail may launch"); return 0 })
	}
	cases := []struct {
		name  string
		setup func(t *testing.T)
		bin   string
		// lead is how the stop's own line starts; want is the step the line after it states, with
		// <config> for the user config's path in the case's HOME.
		lead, want string
	}{
		{"a name that is not a program", func(t *testing.T) {
			captureFixtureHome(t, captureFixtureInstaller)
			noJail(t)
		}, "a/b", `yolo capture: "a/b" is not a program name`,
			"  A capture records what your packs install with an installer or build from a fork: " +
				"probetool. Run: yolo capture probetool"},
		{"a program no selected pack installs", func(t *testing.T) {
			captureFixtureHome(t, captureFixtureInstaller)
			noJail(t)
		}, "nosuchtool", `yolo capture: no selected pack installs "nosuchtool"`,
			"  A capture records what your packs install with an installer or build from a fork: " +
				"probetool. Run: yolo capture probetool"},
		{"a program a pack yolo ships installs", func(t *testing.T) {
			captureFixtureHome(t, captureFixtureInstaller)
			noJail(t)
		}, "codex", `yolo capture: no selected pack installs "codex"`,
			`  The codex pack yolo ships installs codex: add "codex" to "packs" in ` +
				"<config>, then: yolo capture codex"},
		// A shipped pack that installs the program FROM NPM leads to that pack and a launch, never
		// to a capture: it pointed at "yolo capture probetool", a different program.
		{"a program a pack yolo ships installs from npm", func(t *testing.T) {
			captureFixtureHome(t, captureFixtureInstaller)
			noJail(t)
		}, "opencode", `yolo capture: no selected pack installs "opencode"`,
			`  The opencode pack yolo ships installs opencode from npm, which a launch does itself, so ` +
				`it needs no capture: add "opencode" to "packs" in <config>, then: yolo -- opencode`},
		// copilot moved to its vendor's installer (OQ-NI1), so it is codex's case now: a capture.
		{"a program a pack yolo ships installs with an installer", func(t *testing.T) {
			captureFixtureHome(t, captureFixtureInstaller)
			noJail(t)
		}, "copilot", `yolo capture: no selected pack installs "copilot"`,
			`  The copilot pack yolo ships installs copilot: add "copilot" to "packs" in ` +
				"<config>, then: yolo capture copilot"},
		{"an npm program", func(t *testing.T) {
			captureFixtureHome(t, `{"kind":"program","bin":"probetool","via":"npm","package":"probetool@1.0.0"}`)
			noJail(t)
		}, "probetool", `yolo capture: no selected pack installs "probetool"`,
			"  A launch installs it from npm itself, and needs no capture: yolo -- probetool"},
		// No pack installs anything with an installer: the step is the manifest line that declares
		// one. It cited `yolo pack --help`, which does not document it.
		{"no pack that installs with an installer", func(t *testing.T) {
			captureFixtureHome(t, `{"kind":"program","bin":"probetool","via":"npm","package":"probetool@1.0.0"}`)
			noJail(t)
		}, "nosuchtool", `yolo capture: no selected pack installs "nosuchtool"`,
			`  None of your packs installs a program with an installer, so nothing here needs a ` +
				`capture. A pack declares one in its pack.json as {"kind": "program", "bin": ` +
				`"<name>", "via": "installer", "url": "<its install script>"}.`},
		// A name a capture refuses, under a config it cannot read: the programs it takes are not
		// known, and the step said "None of your packs installs a program with an installer".
		{"a name that is not a program, with a config that cannot be read", func(t *testing.T) {
			home := captureFixtureHome(t, captureFixtureInstaller)
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs": [`)
			noJail(t)
		}, "a/b", `yolo capture: "a/b" is not a program name`,
			"  Your config could not be read, so the programs a capture takes are not known: "},
		{"a pack that could not be resolved", func(t *testing.T) {
			home := captureFixtureHome(t, captureFixtureInstaller)
			gone := filepath.Join(home, "packs", "gone")
			cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
			data, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, cfg, strings.Replace(string(data), `"packs": [`,
				`"packs": [{"source": "file://`+gone+`", "name": "gone"}, `, 1))
			noJail(t)
		}, "nosuchtool", "  could not be resolved, so not searched: gone",
			"    Fix the pack at file://"},
		{"a config that cannot be read", func(t *testing.T) {
			home := captureFixtureHome(t, captureFixtureInstaller)
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs": [`)
			noJail(t)
		}, "probetool", "yolo capture: ",
			"  Fix what it names in <config>, then: yolo capture probetool"},
		{"a store it cannot stage in", func(t *testing.T) {
			home := captureFixtureHome(t, captureFixtureInstaller)
			writeFile(t, filepath.Join(paths.CapturesDirUnder(home), "staging"), "not a directory\n")
			noJail(t)
		}, "probetool", "yolo capture: ",
			"  Fix what it names, then run `yolo capture probetool` again."},
		{"a capture jail that failed", func(t *testing.T) {
			captureFixtureHome(t, captureFixtureInstaller)
			withFakeCaptureJail(t, func(run.Options) int { return 7 })
		}, "probetool", "yolo capture: the capture jail exited 7",
			"  Its output above says why: fix what it names, then run `yolo capture probetool` again."},
		{"an installer that left nothing", func(t *testing.T) {
			captureFixtureHome(t, captureFixtureInstaller)
			var seen run.Options
			withFakeCaptureJail(t, fakeCaptureJail(t, &seen, nil))
		}, "probetool", "yolo capture: probetool's installer left nothing",
			"  If `packages` in your config lists probetool, every jail has it already and it needs " +
				"no capture. Otherwise pack fixture's installer writes somewhere else: tell that " +
				"pack's author, or, if yolo ships pack fixture, report it at " + entrypoint.IssuesURL + "."},
		{"a receipt it could not write", func(t *testing.T) {
			captureFixtureHome(t, captureFixtureInstaller)
			var seen run.Options
			fill := fakeCaptureJail(t, &seen, []capture.ManifestEntry{
				{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 8},
			})
			withFakeCaptureJail(t, func(o run.Options) int {
				rc := fill(o)
				// A directory where the receipt goes: the entry is admitted, the append fails.
				if err := os.MkdirAll(capture.ReceiptsPath(filepath.Join(o.Workspace, captureOutLeaf)), 0o755); err != nil {
					t.Fatal(err)
				}
				return rc
			})
		}, "probetool", "yolo capture: writing the capture receipt",
			"  The entry is stored, and a launch finds it by its receipt: fix what it names, then " +
				"run `yolo capture probetool` again, which writes the receipt."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.setup(t)
			want := strings.ReplaceAll(c.want, "<config>", paths.UserConfigPath())
			rc, stderr := runCaptureFor(t, c.bin)
			if rc == 0 {
				t.Fatalf("the capture succeeded:\n%s", stderr)
			}
			if got := stepAfter(t, stderr, c.lead); !strings.HasPrefix(got, want) {
				t.Errorf("the step after %q is\n%q\nwant one starting\n%q\n%s", c.lead, got, want, stderr)
			}
		})
	}
}

// Another capture of the same program holds the lock: wait for it, and run this one again only
// if that one fails.
func TestACaptureRefusedByTheLockNamesItsNextStep(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	held := tryFlockAt(captureLockPath("probetool"))
	if held == nil {
		t.Skip("flock is a no-op on this filesystem")
	}
	defer held.Close()
	if probe := tryFlockAt(captureLockPath("probetool")); probe != nil {
		probe.Close()
		t.Skip("this filesystem does not make a second flock on the same file conflict")
	}
	withFakeCaptureJail(t, func(run.Options) int { t.Error("no jail may launch"); return 0 })
	_, stderr := runCaptureFor(t, "probetool")
	const want = "  Wait for it to finish: what it stores serves every launch on this machine. If it " +
		"fails, run `yolo capture probetool` again."
	if got := stepAfter(t, stderr, "yolo capture: another capture of probetool"); got != want {
		t.Errorf("the step is\n%q\nwant\n%q", got, want)
	}
}

// A FORK'S PROGRAM: its capture is its build, and each of the build's stops names a step too.
func TestEveryForkCaptureStopNamesItsNextStep(t *testing.T) {
	t.Run("no pin", func(t *testing.T) {
		forkHostFixture(t, "probetool", captureFixtureInstaller)
		withFakeCaptureJail(t, func(run.Options) int { t.Error("no jail may launch"); return 1 })
		_, stderr := runCaptureFor(t, "probetool")
		if got, want := stepAfter(t, stderr, "yolo capture: fork forkpack"),
			"  then: yolo capture probetool"; got != want {
			t.Errorf("the step after the pin is %q, want %q\n%s", got, want, stderr)
		}
	})
	t.Run("another build running", func(t *testing.T) {
		f := forkBuildHome(t)
		withFakeCaptureJail(t, func(run.Options) int { t.Error("a build ran under a held lock"); return 1 })
		b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
		holder, err := pidlock.Acquire(b.lockPath(), pidlock.NoWait, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Release()
		_, stderr := runCaptureFor(t, "probetool")
		const want = "  Wait for it to finish: what it stores serves every launch on this machine. If it " +
			"fails, run `yolo capture probetool` again."
		if got := stepAfter(t, stderr, "yolo capture: another build of this fork is running"); got != want {
			t.Errorf("the step is\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("a build that failed", func(t *testing.T) {
		forkBuildHome(t)
		// The toolchain record is the build script's first line, so a jail that wrote it ran its build.
		withFakeCaptureJail(t, func(o run.Options) int {
			writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity\n")
			return 3
		})
		_, stderr := runCaptureFor(t, "probetool")
		const want = "  Its output above says why: fix what it names, then run `yolo capture probetool` again."
		if got := stepAfter(t, stderr, "yolo capture: the capture jail exited 3"); got != want {
			t.Errorf("the step is\n%q\nwant\n%q", got, want)
		}
	})
	// A JAIL THAT STOPPED BEFORE ITS BUILD LINE (PPX-D39) is relayed with the last line it printed,
	// its own refusal, through the writers the build act handed it, on a line of its own under the
	// stop (PPX-D42): no runtime is blamed, and the step follows it. Red if the act stops teeing the
	// jail's writers.
	t.Run("a build jail that refused before its build line", func(t *testing.T) {
		forkBuildHome(t)
		withFakeCaptureJail(t, func(o run.Options) int {
			fmt.Fprintln(o.Stderr, "Flake source: /nix/fixture (YOLO_REPO_ROOT)") // the stream a launch prints it on
			fmt.Fprintln(o.Stdout, "packs: pack forkpack: briefing `agents` names \"nope\", which no pack in `packs` provides")
			return 1
		})
		_, stderr := runCaptureFor(t, "probetool")
		const lead = "yolo capture: its build jail exited 1 before its build line ran"
		const said = "    packs: pack forkpack: briefing `agents` names \"nope\", which no pack in `packs` provides"
		const want = "  Fix what it names, then run `yolo capture probetool` again."
		if got := stepAfter(t, stderr, lead); got != said {
			t.Errorf("the jail's line is\n%q\nwant\n%q", got, said)
		}
		if got := stepAfter(t, stderr, said); got != want {
			t.Errorf("the step is\n%q\nwant\n%q", got, want)
		}
		if strings.Contains(stderr, "runtime") {
			t.Errorf("the stop blames the runtime for the jail's own refusal:\n%s", stderr)
		}
	})
}

// On macos-user a fork's build is refused: it is yolo's to wire, and a podman jail builds and runs
// one today.
func TestTheMacosUserForkRefusalNamesWhoCanAct(t *testing.T) {
	forkBuildHome(t)
	var seen run.Options
	withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 1 })
	var out, errw bytes.Buffer
	captureHost([]string{"probetool"}, &out, &errw, false)
	if seen.MacosUserRun == nil || !seen.Sealed {
		t.Fatal("the fork's build jail carries no macos-user arm")
	}
	// The arm the pipeline runs on macos-user, driven as the pipeline would.
	errw.Reset()
	rc := seen.MacosUserRun(jsonx.NewOrderedMap(), "", nil, nil, "", "", macosuser.HomeOverlay{},
		macosuser.HostContext{}, true, jsonx.NewOrderedMap(), nil, macosuser.JailDaemons{})
	if rc == 0 {
		t.Fatal("a fork build ran on macos-user")
	}
	if got, want := stepAfter(t, errw.String(), "yolo capture: a fork is built on a container backend only"),
		"  That is yolo's to wire. A podman jail builds and runs it today: YOLO_RUNTIME=podman yolo -- probetool"; got != want {
		t.Errorf("the step is\n%q\nwant\n%q", got, want)
	}
}

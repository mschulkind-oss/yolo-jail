package cli

// patchedtreemac_test.go pins a PATCHED EXTENSION's build on a Mac (docs/reference/pi-extensions.md
// §7.1, §8.3, §11; PPX-D5) at the call sites the fork lane's Mac tests pin for a patched fork
// (patchedforkmac_test.go, hostfloorpatchedmac_test.go): the workspace a tree's admit reads its links
// against, and the platform and runtime a Mac host opens a tree's advance with — the macos-user
// fork-build act's, for darwin, as the Mac host floor's patched fork — and what a Mac that cannot run
// that act yet says instead.

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// productionHostTreesBuild is the production predicate, kept before any test stands a host in.
var productionHostTreesBuild = hostTreesBuild

// asMacTreeHost stands this machine in as a Mac host whose macos-user act is as m has it set up,
// deciding whether it builds trees by the production predicate.
func asMacTreeHost(t *testing.T, m macSetup) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	onFloorPlatform(t, "darwin/arm64")
	withMac(t, m)
	prev := hostTreesBuild
	hostTreesBuild = productionHostTreesBuild
	t.Cleanup(func() { hostTreesBuild = prev })
}

// A TREE'S ADMIT READS ITS LINKS AGAINST THE BUILD'S OWN WORKSPACE (forkBuildWorkspace): a link
// into a macos-user build's checkout, in its staging tree, is refused, and the same link read against
// a container build's /workspace is not. Have treeAdmitProblem read /workspace again and the
// macos-user case is admitted.
func TestATreesAdmitReadsLinksAgainstTheBuildsWorkspace(t *testing.T) {
	ws := forkBuildWorkspace("macos-user", "0123456789abcdef")
	reserved := packdecl.TreeReservedDir("tool-ext")
	m := &capture.Manifest{Home: macosuser.CaptureStagingHome(ws), RefScan: capture.RefScanFull,
		Entries: []capture.ManifestEntry{
			{Path: reserved, Kind: capture.KindDir},
			{Path: reserved + "/node_modules/dep", Kind: capture.KindSymlink,
				Target: macosuser.ForkBuildSourceDir(ws) + "/dep"},
		}}
	f := packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".pi/extensions/tool-ext"}
	if why := treeAdmitProblem(m, f, ws); !strings.Contains(why, "a link into its own workspace") {
		t.Errorf("a macos-user tree's link into its checkout was admitted: %q", why)
	}
	if why := treeAdmitProblem(m, f, containerWorkspace); why != "" {
		t.Errorf("read against a container build's workspace, the link was refused: %q", why)
	}
}

// A MAC HOST BUILDS ITS PATCHED EXTENSIONS through the macos-user fork-build act, for darwin, as
// its host floor builds a patched fork (floorPatchedPlatform, floorPatchedRuntime): `yolo host apply
// --assert`'s advance opens with that platform and runtime. Have advanceHostTrees hand the container
// jail's platform, or no runtime, and this fails; on Linux both stay what they were.
func TestAMacHostsTreeAdvanceRunsAsTheSandboxAccount(t *testing.T) {
	for _, c := range []struct {
		name, platform, runtime string
		mac                     bool
	}{
		{"mac", "darwin/arm64", "macos-user", true},
		{"linux", captureJailPlatform(), "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			newTreeFixture(t, `"f.txt"`)
			if c.mac {
				asMacTreeHost(t, macSetup{account: true, privilege: true})
				if !hostTreesBuild() {
					t.Fatal("a set-up Mac builds no tree")
				}
			}
			var got []advanceOptions
			prev := hostTreeAdvance
			hostTreeAdvance = func(_ packload.Fork, o advanceOptions) advanceResult {
				got = append(got, o)
				return advanceResult{}
			}
			t.Cleanup(func() { hostTreeAdvance = prev })
			var errw bytes.Buffer
			if !advanceHostTrees(&errw, false, "", nil) {
				t.Fatalf("the advance refused:\n%s", errw.String())
			}
			if len(got) != 1 {
				t.Fatalf("%d advances, want one\n%s", len(got), errw.String())
			}
			if o := got[0]; o.platform != c.platform || o.runtime != c.runtime || !o.host || !o.launch {
				t.Errorf("the advance opened with platform %q runtime %q (host=%v launch=%v), want %q %q",
					o.platform, o.runtime, o.host, o.launch, c.platform, c.runtime)
			}
		})
	}
}

// A MAC HOST READS WHICH BUILD SERVES A TREE under the same platform and runtime it builds with
// (hostTreeServing, newAdvance), so the render links the darwin build the Mac made.
func TestAMacHostReadsTheTreeBuildItMakes(t *testing.T) {
	newTreeFixture(t, `"f.txt"`)
	asMacTreeHost(t, macSetup{account: true, privilege: true})
	fx := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	if len(fx) != 1 {
		t.Fatalf("%d trees selected, want one", len(fx))
	}
	var got []advanceOptions
	prev := hostTreeNewAdvance
	hostTreeNewAdvance = func(_ packload.Fork, o advanceOptions) (*advance, *advanceResult) {
		got = append(got, o)
		return nil, &advanceResult{}
	}
	t.Cleanup(func() { hostTreeNewAdvance = prev })
	hostTreeServing(fx[0])
	if len(got) != 1 || got[0].platform != "darwin/arm64" || got[0].runtime != "macos-user" {
		t.Errorf("the serving read opened %+v, want darwin/arm64 under macos-user", got)
	}
}

// A MAC THAT CANNOT RUN THE ACT YET builds no tree, and says why with the step: before
// `yolo yolo-account create`, the render and the launch name that setup rather than a jail. Drop
// hostTreeBuildBlocked from hostTreesBuild and the Mac advances a build that cannot run.
func TestAMacThatCannotRunTheBuildActNamesTheSetup(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, treeHostOwn+`,"host_apply_on_launch":false`)
	asMacTreeHost(t, macSetup{})
	if hostTreesBuild() {
		t.Fatal("a Mac before its setup builds trees")
	}
	advanceHostTrees(io.Discard, false, "", nil)
	if len(fx.builds) != 0 {
		t.Error("a Mac that cannot run the act built a tree")
	}
	step := "the yolo account " + macosuser.SandboxUser + ", which a patched extension's build on a Mac runs " +
		"as, does not exist — run the one-time setup, `yolo yolo-account create`, and the next `yolo host` launch builds it"
	if out := fx.renderTrees(t, true); !strings.Contains(out, step) || strings.Contains(out, "YOLO_SETUP=podman-self") {
		t.Errorf("the Mac's render does not name the setup:\n%s", out)
	}
	stubBins(t, "tool")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d, execed %v\n%s", rc, got.execed, errw.String())
	}
	if s := errw.String(); !strings.Contains(s, "is not delivered on this host, so tool starts without it") ||
		!strings.Contains(s, step) {
		t.Errorf("the Mac's launch line:\n%s", s)
	}
}

package cli

// hostfloorpacktreemac_test.go pins a Mac's `yolo host` floor arm for a PACK-TREE PROGRAM
// (docs/design/pack-buildable-programs.md §9's `yolo host`, macOS row; the plan's Task 10): the floor's
// resolver builds it in the macos-user sealed act, as a Mac floor builds a fork; a declared darwin
// prebuilt never stands in for the build (OQ-BG2, ruled 2026-10-23), and is taken only by the user's
// opt-in (BG-D18).

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestAMacFloorBuildsAPackTreeProgramInTheDarwinAct(t *testing.T) {
	f, digest := packTreeBuildFixture(t, "make")
	pack := &packload.Pack{Name: "fx", Root: f.Root, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindProgram, Bin: "x", Via: packdecl.ViaPack, From: "tools/x", Build: "make", Run: "x",
		Prebuilt: map[string]packdecl.PrebuiltBuild{"darwin/arm64": {URL: "https://example.invalid/x",
			SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}},
	}}}}
	prog := hostfloor.Program{Pack: "fx", Install: pack.Decl.InstallContributions()[0]}
	prevPlatform := hostFloorCapturePlatform
	hostFloorCapturePlatform = func() string { return "darwin/arm64" }
	t.Cleanup(func() { hostFloorCapturePlatform = prevPlatform })
	var runtimes []string
	prevAct := floorBuildFork
	floorBuildFork = func(_ forkBuild, m buildMode, _, _ io.Writer, _ bool) (*capture.Entry, error) {
		runtimes = append(runtimes, m.runtime)
		return nil, errors.New("stand-in act")
	}
	t.Cleanup(func() { floorBuildFork = prevAct })
	store := &capture.Store{Dir: paths.CapturesDir()}

	// A Mac whose darwin act can run (here, the account mode with the sandbox account): the tree is
	// built, for darwin, in the macos-user sealed act — never the prebuilt, though one is declared.
	withMac(t, macSetup{account: true})
	d := floorPackTree(prog, pack, store, io.Discard)
	if d.Reason != "" || d.Revision != digest || !d.Jail || d.Disclosure != "" {
		t.Fatalf("a buildable Mac floor resolved %+v, want the build at the tree digest %s", d, digest)
	}
	if _, err := d.Build(); err == nil || len(runtimes) != 1 || runtimes[0] != "macos-user" {
		t.Errorf("the floor's build ran under runtimes %q (%v), want the macos-user act once", runtimes, err)
	}

	// The run-as-you mode needs no account, so it builds too.
	self := macSetup{}.probes()
	self.Mode = macosuser.ModeSelf
	prev := hostFloorMacProbes
	hostFloorMacProbes = func() macCaptureProbes { return self }
	if d := floorPackTree(prog, pack, store, io.Discard); d.Revision != digest || d.Disclosure != "" {
		t.Errorf("a run-as-you Mac floor resolved %+v, want the build", d)
	}
	hostFloorMacProbes = prev

	// An account-mode Mac with no sandbox account can never build — and by OQ-BG2's ruling the darwin
	// prebuilt does NOT stand in: the delivery is still the build, whose cannot-build refusal (the
	// floor's) names the setup step and then this opt-in.
	withMac(t, macSetup{})
	d = floorPackTree(prog, pack, store, io.Discard)
	if d.Reason != "" || !d.Jail || d.Disclosure != "" || d.Revision != digest {
		t.Errorf("an unbuildable Mac floor resolved %+v, want the build (refused by the floor), never the prebuilt", d)
	}
	if !strings.Contains(d.PrebuiltOptInStep, "`yolo config set programs.overrides.x.prebuilt true --user`") {
		t.Errorf("the build's opt-in clause = %q, want the prebuilt opt-in for x", d.PrebuiltOptInStep)
	}

	// THE USER'S OPT-IN (BG-D18) takes the darwin prebuilt, source present, with its which-bytes line
	// and no jail. Delete the floor's read of config.ProgramPrebuiltOptIns and this goes red.
	writeUserConfig(t, `{"programs": {"overrides": {"x": {"prebuilt": true}}}}`)
	d = floorPackTree(prog, pack, store, io.Discard)
	if d.Reason != "" || d.Jail || !strings.Contains(d.Disclosure, "programs.overrides.x.prebuilt") ||
		d.Revision != pack.Decl.Contributes[0].Prebuilt["darwin/arm64"].SHA256 {
		t.Errorf("an opted-in Mac floor resolved %+v, want the darwin prebuilt, disclosed", d)
	}

	// A DEV OVERRIDE'S CHECKOUT on the same unbuildable Mac: the build is the checkout's, and its
	// refusal names no prebuilt opt-in, which `path` cannot be combined with (BG-D21).
	dev := floortest.ResolvedTemp(t)
	writeUserConfig(t, `{"programs": {"overrides": {"x": {"path": "`+dev+`"}}}}`)
	d = floorPackTree(prog, pack, store, io.Discard)
	if d.Reason != "" || !d.Jail || !strings.Contains(d.Disclosure, dev) || d.PrebuiltOptInStep != "" {
		t.Errorf("a checkout on an unbuildable Mac floor resolved %+v, want its build and no opt-in clause", d)
	}
}

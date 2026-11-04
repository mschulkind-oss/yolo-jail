package cli

// hostfloorpacktree.go wires a PACK-TREE PROGRAM (docs/design/pack-buildable-programs.md, `via:
// "pack"`) into the host floor (§9's `yolo host` rows; the plan's Tasks 9 and 10 — on a Mac the build
// is the macos-user sealed act, as a fork's floor build is): the floor's
// PackTree resolver (hostfloor's packtree.go) is answered here, through the same resolution and the
// same build act a jail launch and `yolo host apply` share with `yolo pack build`.
//
//   - THE TREE IS THE STAGED ONE. The pack is staged into this process's pack tree exactly as a launch
//     stages it (resolveStagedPack, `yolo pack build`'s resolver), and its `from` digested there, so the
//     floor's key is the key a jail launch on this machine looks up, and the build is handed the bytes
//     the digest read (§6.1). A local pack read in place would key a build no launch ever asks for.
//   - WHICH BYTES is packload.PackTreeResolve's answer for the floor's platform: the build of the tree,
//     or the prebuilt artifact where the pack ships no source or the user opted into it
//     (`programs.overrides.<bin>: {"prebuilt": true}`, BG-D18). An artifact never stands in for a
//     present source on yolo's own (OQ-BG2): a build that cannot run here — no runtime on Linux, no
//     sandbox account on a Mac — is the floor's refusal, which names the setup step and the opt-in.
//   - THE USER'S CHECKOUT (`programs.overrides.<bin>: {"path": …}`, §5, BG-D19) replaces the staged
//     `from` exactly as at a jail launch (run.ResolvePackTreeProgram): digested where it sits on disk
//     at this resolution, built when it changed and reused when it did not, with a line naming it on
//     every launch while it serves.
//   - THE ACT is buildFork, the one every launch runs: the sealed build jail, the host's copy of a tree
//     with no build line, or the verified fetch, under the build's lock. Its failure names the build's
//     log and the retry, `yolo pack build <pack> --setup host-self` (§8).

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// floorStagePack resolves a selected pack by name, staged as a launch stages it. A var so a test can
// hand the floor a pack of its own.
var floorStagePack = func(name string) (*packload.Pack, error) {
	entries, _, err := config.LoadPackEntries()
	if err != nil {
		return nil, err
	}
	entry := config.EmbeddedPackEntry(name)
	for _, e := range entries {
		if e.Name == name {
			entry = e
			break
		}
	}
	return resolveStagedPack(entry)
}

// floorBuildFork is the build act the floor's pack-tree programs run (buildFork), a var so a test can
// stand a fake act in.
var floorBuildFork = buildFork

// floorPackTrees is a floor's PackTree resolver: each pack staged at most once per floor, however many
// of its programs ask.
func floorPackTrees(store *capture.Store, out io.Writer) func(p hostfloor.Program) hostfloor.PackTreeDelivery {
	var mu sync.Mutex
	staged := map[string]*packload.Pack{}
	stageErr := map[string]error{}
	return func(p hostfloor.Program) hostfloor.PackTreeDelivery {
		mu.Lock()
		pack, seen := staged[p.Pack]
		err := stageErr[p.Pack]
		if !seen && err == nil {
			pack, err = floorStagePack(p.Pack)
			staged[p.Pack], stageErr[p.Pack] = pack, err
		}
		mu.Unlock()
		if err != nil {
			return hostfloor.PackTreeDelivery{Reason: "pack " + p.Pack + " could not be staged to read " + p.Bin() +
				"'s source (" + err.Error() + ") — fix what that names, then run `yolo host` again"}
		}
		return floorPackTree(p, pack, store, out)
	}
}

// floorPackTree is one pack-tree program's delivery on the floor, from its pack staged.
func floorPackTree(p hostfloor.Program, pack *packload.Pack, store *capture.Store, out io.Writer) hostfloor.PackTreeDelivery {
	var fork *packload.Fork
	for _, f := range packload.PackTreePrograms([]*packload.Pack{pack}) {
		if f.Bin == p.Bin() {
			f := f
			fork = &f
			break
		}
	}
	if fork == nil {
		return hostfloor.PackTreeDelivery{Reason: "pack " + p.Pack + " no longer declares " + p.Bin() +
			" as a program built from its own tree — run `yolo host` again to read its current declaration"}
	}
	platform := hostFloorCapturePlatform()
	// A MAC'S FLOOR builds in the macos-user sealed act (FP-D24), as its forks do (wireFloorBuild).
	// Where that act cannot run — an account-mode Mac with no sandbox account among its refusals — the
	// floor's own cannot-build refusal applies (BuildActUnavailable, macBuildBlocked), and it names
	// the prebuilt opt-in when the pack declares an artifact here (PrebuiltOptInStep). A prebuilt is
	// never substituted for a present source (OQ-BG2): only the user's opt-in (BG-D18) or an absent
	// source takes it.
	onMac := strings.HasPrefix(platform, "darwin/")
	pin := run.ResolvePackTreeProgram(*fork, platform, config.ProgramPrebuiltOptIns(nil)[fork.Bin], config.ProgramOverrides(nil))
	if pin.Reason != "" {
		return hostfloor.PackTreeDelivery{Reason: pin.Reason}
	}
	b := forkBuild{Fork: pin.Fork, Commit: pin.Commit, Platform: platform}
	f := b.Fork
	d := hostfloor.PackTreeDelivery{
		Revision: b.Commit,
		Recipe:   b.recipe(),
		What:     packTreeWhat(b),
		Version:  "tree " + shortSHA(b.Commit),
		Jail:     !f.IsPrebuilt() && strings.TrimSpace(f.Build) != "",
	}
	if d.Jail {
		// Every refusal of this build names the opt-in where one is declared (BG-D21); a dev override's
		// checkout names none, since `path` cannot be combined with `prebuilt` (Fork.PrebuiltOptInStep).
		d.PrebuiltOptInStep = f.PrebuiltOptInStep(platform)
	}
	if f.IsPrebuilt() {
		d.Version = "prebuilt sha256 " + shortSHA(b.Commit)
		d.Disclosure = run.PrebuiltLine(f.Bin, f.Pack, f.PrebuiltWhy, f.Prebuilt.SHA256, f.Prebuilt.URL)
	}
	if f.Checkout != "" {
		d.Version = "checkout tree " + shortSHA(b.Commit)
		d.Disclosure = run.CheckoutLine(f.Bin, f.Checkout, "at tree digest "+shortSHA(b.Commit)+
			"; an edit there is built at the next `yolo host` launch")
	}
	d.Hit = func() (*capture.Entry, error) {
		entry, _, err := resolveForkBuild(store, f.Bin, b.Platform, b.selectSource(), b.Commit, b.recipe())
		return entry, err
	}
	d.Build = func() (*capture.Entry, error) {
		mode := buildMode{lock: pidlock.Mode{Wait: true, Bound: forkBuildWaitBound},
			jailStdout: hostJailStdout(), keepJailOutput: true}
		if onMac {
			mode.runtime = "macos-user"
		}
		entry, err := floorBuildFork(b, mode, out, out, false)
		if err != nil {
			return nil, floorPackTreeFailure(b, store, err)
		}
		return entry, nil
	}
	return d
}

// floorPackTreeFailure is a failed floor build's error in §8's terms: what failed, the build's log
// when it kept one, and the retry on the host. The readiness act's refusal adds the bypass.
func floorPackTreeFailure(b forkBuild, store *capture.Store, err error) error {
	why := err.Error()
	if log := buildJailLogPath(store.StagingDir("fork-" + b.id())); fileExists(log) {
		why += withBuildLog(log)
	}
	verb := "building"
	switch {
	case b.Fork.IsPrebuilt():
		verb = "fetching"
	case strings.TrimSpace(b.Fork.Build) == "":
		verb = "copying"
	}
	return fmt.Errorf("%s %s from %s failed: %s — retry: `yolo pack build %s --setup host-self`", verb, b.Fork.Bin,
		packTreeWhat(b), why, b.Fork.Pack)
}

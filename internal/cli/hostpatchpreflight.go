package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostpatchpreflight.go is PF-D81's CHECK BEFORE A HOST WRITE (docs/design/patched-forks.md §8): a
// host verb that renders into the real home — `yolo host -- <bin>` under host_apply_on_launch, whose
// launch gate may apply, and `yolo host apply --assert` — asks each selected patched fork's program
// whether a patch failure is already recorded for its series as it stands, BEFORE the first write.
// A failure the patch failure's own bypass does not continue past refuses the verb there, with the
// error block, so a refused verb leaves nothing half-written: no render ran.
//
// It reads; it builds and checks nothing (hostfloor.Floor.PreparePatched with no advance): the
// fork's advance stays where it ran before, after the render gate (the floor stage, or the launch's
// readiness act and target), so a failure that advance FIRST finds stops the verb there — after a
// render that does not depend on the fork's build, and before the floor entry is written. A patched
// extension's advance already runs before the render (advanceHostTrees), so its failure stops there.

// hostPatchPreflightFloor is the floor the check asks: newHostFloor, read at each call, so the check
// asks the floor every other host verb builds (a test's own floor included) rather than the one
// newHostFloor was when the package initialized.
func hostPatchPreflightFloor(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
	return newHostFloor(out, progs)
}

// hostPatchPreflight returns false, having printed the refusal on errw, when a selected patched
// fork's recorded patch failure stops verb before its first write. command is the operation the
// error block's bypass names. target is the program a `yolo host --` launch runs, "" for an apply: at
// a launch, a failure that leaves nothing of another program to run is the readiness act's to waive
// under the missing-program hatch (PF-D83), so it does not stop the launch here while that hatch is
// set.
//
// deferred is an apply that advances nothing, the one `yolo pack update` runs: there the patched
// extensions' advance does not run before the render either, so their recorded failures are read here
// too, and no error block is said again, the update's own check having said each just before.
func hostPatchPreflight(errw io.Writer, packs []*packload.Pack, verb, command, target string, deferred bool,
	act *run.ActInterrupt) bool {
	if config.InJail() {
		return true
	}
	progs := floorPrograms(packs)
	var said bytes.Buffer
	floor := hostPatchPreflightFloor(&said, progs)
	floor.PatchBypassCommand = command
	ctx := withActInterrupt(context.Background(), act)
	missingHatch := os.Getenv(paths.AllowMissingProgramsEnv) != ""
	var stopped []string
	for _, p := range progs {
		if p.Install.Patches == "" || floor.OutsideTheFloor(p) {
			continue
		}
		_, err := floor.PreparePatched(ctx, p, false)
		var failure *packsrc.PatchFailure
		if !errors.As(err, &failure) {
			continue // no recorded patch failure: whatever else is wrong, the later act says it
		}
		if target != "" && p.Bin() != target && missingHatch && errors.Is(err, hostfloor.ErrPatchFailureNothingRuns) {
			continue
		}
		stopped = append(stopped, "fork "+p.Install.ForkedBy+"/"+p.Bin())
	}
	if deferred && hostTreesBuild() {
		for _, f := range packload.PatchedTrees(packs) {
			if !f.DeliveredAtHost() || currentPatchFailure(f) == nil {
				continue
			}
			if entry, _, _ := hostTreeServing(f); entry != nil && allowPatchFailures() {
				continue // the bypass: the render links the good build, as it does for an advance's
			}
			stopped = append(stopped, f.Label())
		}
	}
	if len(stopped) == 0 {
		return true
	}
	if !deferred {
		_, _ = io.Copy(errw, &said)
	}
	fmt.Fprintf(errw, "yolo host: refusing %s: the patch series of %s %s not apply (the ERROR above); nothing was "+
		"written.\n", verb, entrypoint.JoinAnd(stopped), plural(len(stopped), "does", "do"))
	return false
}

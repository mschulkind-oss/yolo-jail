package cli

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// prepareProgramCapture is the ordinary admission adapter. It does no source checking,
// rendering or installation itself: the selected producer owns the sealed act, and guard
// revalidates its selected inputs at publication using the scheduler/request's guard.
// Until that producer is wired, supported launchers keep their legacy route.
func prepareProgramCapture(ctx context.Context, store *capture.Store, inputs capture.ProgramInputs,
	artifacts []string, produce func(context.Context, string) error, guard func(func() error) error) (*capture.Entry, error) {
	identity, err := inputs.Identity()
	if err != nil {
		return nil, err
	}
	if produce == nil || guard == nil {
		return nil, fmt.Errorf("program capture: no selected producer/admission guard — use the supported installer route")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock, err := pidlock.Acquire(filepath.Join(store.Dir, "locks", "program-"+identity+".lock"), pidlock.Mode{Wait: true, Bound: 20 * time.Minute, Cancel: ctx.Done()}, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	defer lock.Release()
	// Always reread the winner after the keyed wait; unchanged resolved inputs do not install again.
	selected, err := capture.Select(store, captureRecords)
	if err != nil {
		return nil, err
	}

	var winner *capture.Entry
	if best, ok := selected[capture.Program{Bin: inputs.Bin, Platform: inputs.Platform, Compatibility: identity}]; ok {
		winner, err = store.Resolve(best.Key)
		if err != nil {
			return nil, err
		}
		m, err := capture.ReadManifest(winner.Root)
		if err != nil {
			return nil, err
		}
		if err := validateProgramManifest(m, inputs.Platform, artifacts); err != nil {
			return nil, err
		}
	}
	var entry *capture.Entry
	err = guard(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry = winner
		return nil
	})
	if err != nil {
		return nil, err
	}
	if entry != nil {
		return entry, nil
	}
	stagingID := "program-" + identity
	staging := store.StagingDir(stagingID)
	cname := stagingJailName(staging)
	if captureWorkspaceArtifactsExist(staging, cname) {
		release, safe := forkBuildWorkspaceReclaimable(staging, cname)
		if !safe {
			return nil, forkBuildRetained{msg: "program capture workspace is not known gone — stop its owned jail, then retry preparation"}
		}
		cleanupForkBuildWorkspace(staging, cname)
		release()
	}
	staging, err = store.Stage(stagingID)
	if err != nil {
		return nil, err
	}
	// The producer's workspace remains under the store until its jail is known gone: an unknown
	// surviving runtime may still own it after cancellation. Admission reclaims it below.
	if err := produce(ctx, staging); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := writeForkBuildRunReturned(staging); err != nil {
		return nil, err
	}
	// The jail is the one the run recorded (stagingJailNameOr), not only the one predicted: the
	// known-gone proof and the reclaim below must name the same jail.
	cname = stagingJailNameOr(staging, cname)
	release, safe := forkBuildWorkspaceReclaimable(staging, cname)
	if !safe {
		return nil, forkBuildRetained{msg: "program capture jail is not known gone — wait for its teardown, then retry preparation"}
	}
	defer release()
	manifest, err := capture.ReadManifest(filepath.Join(staging, captureOutLeaf))
	if err != nil {
		return nil, err
	}
	if len(manifest.Entries) == 0 {
		return nil, fmt.Errorf("selected program producer left no artifacts — retry the selected install")
	}
	if manifest.Platform != inputs.Platform {
		return nil, fmt.Errorf("selected program producer ran on a different platform — resolve its actual runtime and retry")
	}
	if err := validateProgramManifest(manifest, inputs.Platform, artifacts); err != nil {
		return nil, err
	}
	entry, err = store.AdmitEntryValidated(ctx, filepath.Join(staging, captureOutLeaf), func(retained *capture.Manifest) error {
		if err := validateProgramManifest(retained, inputs.Platform, artifacts); err != nil {
			return err
		}
		if manifest.Relocatable != retained.Relocatable || !slices.Equal(sortedProgramRefs(manifest.AbsoluteRefs), sortedProgramRefs(retained.AbsoluteRefs)) {
			return fmt.Errorf("program capture: retained capture has a different relocation contract — use the supported installer route")
		}
		if !retained.Relocatable && retained.Home != manifest.Home {
			return fmt.Errorf("program capture: retained capture is tied to another home — use the supported installer route")
		}
		return nil
	}, func(commit func() error) error {
		return guard(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return commit()
		})
	})
	if err != nil {
		return nil, err
	}
	// Admitted: the producer workspace (staging, its jail home and tracking, its owner records)
	// is reclaimed under the known-gone proof still held above, as the fork-build path does.
	// A workspace whose jail was not known gone never reaches here: it is retained above.
	cleanupForkBuildWorkspace(staging, cname)
	receipt := entrypoint.CaptureReceipt{Bin: inputs.Bin, Declared: inputs.Declared, Key: entry.Key, Digest: capture.DigestHash(entry.Digest), Bytes: manifest.TotalBytes(), Path: entry.Root, Platform: inputs.Platform, Compatibility: identity, Time: time.Now()}
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), receipt.Line()); err != nil {
		return nil, err
	}
	return entry, nil
}

func validateProgramManifest(m *capture.Manifest, platform string, artifacts []string) error {
	if m.Platform != platform {
		return fmt.Errorf("program capture: canonical platform %q does not match %q — use the supported installer route", m.Platform, platform)
	}
	if m.RefScan != capture.RefScanFull {
		return fmt.Errorf("program capture: canonical capture lacks the full relocation scan — use the supported installer route")
	}
	return capture.ValidateArtifacts(m, artifacts)
}

func sortedProgramRefs(refs []capture.AbsoluteRef) []capture.AbsoluteRef {
	out := slices.Clone(refs)
	slices.SortFunc(out, func(a, b capture.AbsoluteRef) int {
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return cmp.Compare(a.Value, b.Value)
	})
	return out
}

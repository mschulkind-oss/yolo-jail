package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// wireFloorOrdinaryCapture is compatibility-gated reuse, not a native producer.
// The host must independently observe EVERY producer runtime fact. A host without
// the runtime identity marker (Darwin included) retains its bounded floor installer.
func wireFloorOrdinaryCapture(f *hostfloor.Floor, store *capture.Store, out io.Writer) {
	f.PrepareNPM = func(ctx context.Context, p hostfloor.Program, nodeBin string, fresh bool) (*hostfloor.PreparedNPM, error) {
		in, ok := npmCaptureInputFor(p.Pack, p.Install)
		if !ok {
			return nil, nil
		}
		if f.GOOS != "linux" {
			return nil, nil
		}
		cfg, err := config.UserScopeConfig(true, nil)
		if err != nil {
			return nil, nil
		}
		reason := run.OrdinaryRuntimeUnsupported(cfg, os.Getenv("YOLO_STORE_PACKAGES") == "1")
		for _, key := range []string{"NODE_OPTIONS", "NODE_PATH", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH"} {
			if os.Getenv(key) != "" {
				reason = "ambient " + key + " changes the host runtime environment"
			}
		}
		if loader := os.Getenv("LD_LIBRARY_PATH"); loader != "" && loader != "/usr/local/lib/yolo-ld" {
			reason = "the host loader environment is not the observed baseline"
		}
		if reason != "" {
			fmt.Fprintf(out, "yolo host: %s sealed artifact reuse is unsupported (%s); retaining its authorized bounded host installer — use an independently matching baseline runtime for reuse\n", p.Bin(), reason)
			return nil, nil
		}
		record, err := readNPMCaptureRecord(store, in)
		if err != nil {
			fmt.Fprintf(out, "yolo host: %s capture compatibility could not be proven (%v); retaining the bounded host installer — run a fresh jail launch to repair the capture record\n", p.Bin(), err)
			return nil, nil
		}
		if record.Resolution == nil {
			return nil, nil
		}
		actual, err := observeNPMCaptureRuntime(ctx, f.Home, filepath.Join(nodeBin, "node"))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || actual != record.Resolution.Runtime || actual.Platform != f.GOOS+"/"+f.GOARCH {
			return nil, nil
		}
		inputs, err := record.Resolution.inputs()
		if err != nil {
			return nil, nil
		}
		identity, err := inputs.Identity()
		if err != nil || identity != record.Identity {
			return nil, nil
		}
		// PD-NF1 (docs/design/program-delivery.md §3.6), by the jail launch's own rule
		// (admittedServes, retriedAdmitted): a prepared generation whose last check found a newer
		// version and failed to prepare it is not served in silence. Unless a hold, the hatch or a
		// check that never resolved anything serves it, this act retries the newer version in front
		// of the program.
		var admitted *npmCaptureRecord
		var recovery admittedRecovery
		if !fresh && record.Failure != "" {
			recovery = hostAdmittedRecovery(f, p, in)
			if !admittedServes(in, record, recovery, out) {
				prior := record
				admitted, fresh = &prior, true
			}
		}
		if fresh {
			next, err := hostOrdinaryCheck(ctx, in, actual.Platform, out)
			switch {
			case admitted != nil && err != nil && !errors.Is(err, errNPMCaptureUnsupported) && !errors.Is(ctx.Err(), context.Canceled):
				// The retry failed. A newer version found again is the refusal; a check that found
				// nothing serves the prepared generation, saying it could not check.
				guard := func(commit func() error) error {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return commit()
				}
				if _, err = retriedAdmitted(ctx, store, in, *admitted, err, recovery, out, guard); err != nil {
					var refusal *hostfloor.UpdateRefusal
					if errors.As(err, &refusal) {
						_, _ = io.WriteString(out, refusal.Render(floorOutColor(out)))
					}
					return nil, err
				}
			case errors.Is(err, errSharedPreparationForegroundOnly), errors.Is(err, errNPMCaptureUnsupported):
				return nil, nil
			case err != nil:
				return nil, err
			default:
				record = next
				if record.Resolution == nil || record.Resolution.Runtime != actual {
					return nil, nil
				}
				inputs, err = record.Resolution.inputs()
				if err != nil {
					return nil, err
				}
				identity, err = inputs.Identity()
				if err != nil {
					return nil, err
				}
			}
		}
		entry, _, err := resolveProgramCaptureFor(store, in.Bin, actual.Platform, identity)
		if err != nil {
			return nil, err
		}
		// Selection closes over the exact identity for this floor operation. A pointer
		// admitted by a later launch cannot change its materialization under the lock.
		// The install may run on another Node than the one observed here (a shipped Node bump since
		// the installed record): the selection holds only when that Node matches every producer
		// fact this one did.
		matches := func(ctx context.Context, nodeBin string) (bool, error) {
			other, err := observeNPMCaptureRuntime(ctx, f.Home, filepath.Join(nodeBin, "node"))
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return err == nil && other == actual, nil
		}
		return &hostfloor.PreparedNPM{Compatibility: identity, EntryKey: entry.Key, Version: inputs.Release, Matches: matches, Materialize: func(ctx context.Context, home string) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if rc := materializeCapture(materializeArgs{store: store.Dir, bin: in.Bin, home: home, compatibility: identity, declared: inputs.Declared}, out); rc != 0 {
				return fmt.Errorf("exact host npm materialization failed — run yolo host --update to retry")
			}
			return ctx.Err()
		}}, nil
	}
}

// hostOrdinaryCheck is the host floor's fresh ordinary preparation of in: the check `yolo host
// --update` runs, and the retry PD-NF1 runs over a prepared generation whose newer version failed.
// errSharedPreparationForegroundOnly and errNPMCaptureUnsupported say the floor's bounded installer
// is the route instead.
func hostOrdinaryCheck(ctx context.Context, in npmCaptureInput, platform string, out io.Writer) (npmCaptureRecord, error) {
	d := preparationDemand{Kind: preparationOrdinaryProgramKind, Key: ordinaryProgramKey(in), Runtime: captureRuntime(), Platform: platform}
	current, fingerprint, release, err := resolveOrdinaryPreparation(ctx, d)
	release()
	if err != nil {
		return npmCaptureRecord{}, err
	}
	if current == nil || *current != in {
		return npmCaptureRecord{}, errPreparationAdmissionStale
	}
	d.Identity = ordinaryProgramIdentity(in, fingerprint, d.Runtime, d.Platform)
	guard := func(commit func() error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, _, release, e := resolveOrdinaryPreparation(ctx, d)
		defer release()
		if e != nil {
			return e
		}
		return commit()
	}
	return waitOrdinaryPreparation(ctx, in, fingerprint, d.Runtime, d.Platform, true, out, guard)
}

// hostAdmittedRecovery is the host floor's admittedRecovery for p: the floor's own `agent_updates`
// policy and hatch, its `yolo host` lines, and the floor refusal's repair and hatch commands
// (hostfloor's refuseUpdate and hatchCommand, which are the floor's own and unexported).
func hostAdmittedRecovery(f *hostfloor.Floor, p hostfloor.Program, in npmCaptureInput) admittedRecovery {
	rc := admittedRecovery{Prefix: "yolo host: ", Repair: "yolo host --update -- " + shquote.Quote(p.Bin()),
		Command: f.PatchBypassCommand}
	if rc.Command == "" {
		rc.Command = "yolo host -- " + shquote.Quote(p.Bin())
	}
	rc.Held = !f.ManualUpdate && f.UpdatesAllowed != nil && !f.UpdatesAllowed(p.Pack)
	if f.UseInstalled != nil {
		rc.UseInstalled = f.UseInstalled(p)
	} else {
		rc.UseInstalled = ordinaryUseInstalled(in)
	}
	return rc
}

// floorOutColor is the floor's color decision for its Out (hostfloor's colorFor).
func floorOutColor(w io.Writer) bool {
	file := tty.FileForTTY(w)
	return tty.Color(nil, true, file != nil && tty.IsTerminalFile(file))
}

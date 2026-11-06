package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

const preparationOrdinaryProgramKind = "ordinary-program"

// resolvePreparationBundle is the launch-source dependency used by ordinary preparation.
// Tests with tiny source fixtures replace this seam instead of reviving a runtime selector.
var resolvePreparationBundle = func() (reporoot.Resolution, bool) {
	return reporoot.Resolve(os.Getenv)
}

// ':' is forbidden in pack names, so an ordinary key cannot collide with a source program/tree key.
func ordinaryProgramKey(in npmCaptureInput) string { return "ordinary:" + in.Owner + "/" + in.Bin }
func ordinaryProgramIdentity(in npmCaptureInput, fingerprint, rt, platform string) string {
	b, _ := json.Marshal(struct {
		Input                          npmCaptureInput
		Fingerprint, Runtime, Platform string
	}{in, fingerprint, rt, platform})
	return npmCaptureHash(b)
}

func ordinarySelectionEnvironment(cfg *jsonx.OrderedMap, packs []*packload.Pack) *entrypoint.Env {
	vars := map[string]string{}
	install, _ := cfg.Get("install")
	installMap, _ := install.(*jsonx.OrderedMap)
	var miseTools any
	present := false
	if installMap != nil {
		miseTools, present = installMap.Get("mise")
	}
	if !present {
		miseTools, present = cfg.Get("mise_tools") // direct callers may still supply the legacy shape
	}
	if present {
		vars["YOLO_MISE_TOOLS"] = jsonx.MustDumpsCompact(miseTools)
	}
	raw, _ := cfg.Get("mcp_servers")
	table, _ := raw.(*jsonx.OrderedMap)
	vars["YOLO_MCP_SERVERS"] = jsonx.MustDumpsCompact(packload.ComposeMCPServers(table, packs, "/home/agent"))
	return entrypoint.NewEnv(vars)
}

// platform is the JAIL's `<os>/<arch>`, which judges each vendor's published platforms
// (entrypoint.SelectedHostProgramPreparations), never the host's.
func selectedOrdinaryInputs(e *entrypoint.Env, packs []*packload.Pack, mode entrypoint.ProgramPreparationMode, platform string) ([]npmCaptureInput, error) {
	selected, err := entrypoint.SelectedHostProgramPreparations(e, packs, mode, platform)
	if err != nil {
		return nil, err
	}
	selected = append(selected, entrypoint.SelectedServerPreparations(e, packs, mode)...)
	seen := map[string]bool{}
	var out []npmCaptureInput
	for _, p := range selected {
		in, ok := npmCaptureInputFor(p.Pack, p.Install)
		if !ok || seen[in.Bin] {
			continue
		}
		seen[in.Bin] = true
		out = append(out, in)
	}
	return out, nil
}

type resolvedPreparationDemand struct {
	Fork        packload.Fork
	NPM         *npmCaptureInput
	Fingerprint string
}

func resolvePreparationDemand(ctx context.Context, d preparationDemand) (resolvedPreparationDemand, func(), error) {
	if d.Kind == preparationOrdinaryProgramKind {
		in, fingerprint, release, err := resolveOrdinaryPreparation(ctx, d)
		return resolvedPreparationDemand{NPM: in, Fingerprint: fingerprint}, release, err
	}
	f, release, err := resolveDemandFork(ctx, d)
	return resolvedPreparationDemand{Fork: f}, release, err
}

// resolveOrdinaryPreparation obtains INSTALLATION authority only from current user
// declarations. The requester carries key/identity, never executable paths or a manifest.
func resolveOrdinaryPreparation(ctx context.Context, d preparationDemand) (*npmCaptureInput, string, func(), error) {
	resolver, release := preparationScopedResolver(ctx)
	var contention error
	selected := selectHostPacks(func(entry config.PackEntry) (*packload.Pack, error) {
		p, err := resolver(entry)
		if errors.Is(err, packsrc.ErrLockHeld) {
			contention = err
		}
		return p, err
	}, config.UserScopeSelection())
	if contention != nil {
		return nil, "", release, contention
	}
	if problems := selected.problems(); len(problems) > 0 {
		return nil, "", release, fmt.Errorf("current user selection is incomplete — repair the user config before retrying preparation")
	}
	cfg, err := config.UserScopeConfig(true, nil)
	if err != nil {
		return nil, "", release, err
	}
	if run.OrdinaryRuntimeUnsupported(cfg, os.Getenv("YOLO_STORE_PACKAGES") == "1") != "" {
		return nil, "", release, errSharedPreparationForegroundOnly
	}
	root, ok := resolvePreparationBundle()
	if !ok {
		if root.Error != nil {
			return nil, "", release, fmt.Errorf("ordinary runtime source is unavailable: %w", root.Error)
		}
		return nil, "", release, fmt.Errorf("ordinary runtime source is unavailable — run yolo check to repair its flake source")
	}
	fingerprint, err := run.ProgramRuntimeFingerprint(root.Root, cfg)
	if err != nil {
		return nil, "", release, err
	}
	vector, err := selectedOrdinaryInputs(ordinarySelectionEnvironment(cfg, selected.packs), selected.packs, entrypoint.ProgramInstallOnly, d.Platform)
	if err != nil {
		return nil, "", release, err
	}
	for _, in := range vector {
		if ordinaryProgramKey(in) != d.Key {
			continue
		}
		identity := ordinaryProgramIdentity(in, fingerprint, d.Runtime, d.Platform)
		if d.Identity != "" && identity != d.Identity {
			return nil, "", release, errPreparationAdmissionStale
		}
		if d.Platform != captureJailPlatform() || !strings.HasPrefix(d.Platform, "linux/") {
			return nil, "", release, errSharedPreparationForegroundOnly
		}
		if d.Background && !entrypoint.PackPolicyAllows(config.AgentUpdatesWire(), in.Owner) {
			return nil, "", release, errPreparationAdmissionStale
		}
		if d.SubscriptionGeneration != 0 {
			state, e := loadPreparationState()
			if e != nil {
				return nil, "", release, e
			}
			idx := preparationRecordIndex(state, d.Key, d.Runtime, d.Platform)
			if idx < 0 || !state.Subscriptions[idx].Active || state.Subscriptions[idx].Generation != d.SubscriptionGeneration || state.Subscriptions[idx].InputIdentity != identity {
				return nil, "", release, errPreparationAdmissionStale
			}
		}
		return &in, fingerprint, release, nil
	}
	return nil, "", release, errSharedPreparationForegroundOnly
}

func preparationDemandInputIdentity(resolved resolvedPreparationDemand, d preparationDemand) (string, error) {
	if resolved.NPM != nil {
		return ordinaryProgramIdentity(*resolved.NPM, resolved.Fingerprint, d.Runtime, d.Platform), nil
	}
	f := resolved.Fork
	if f.IsTree() {
		return preparationInputIdentity(f, d.Runtime, d.Platform)
	}
	commit := ""
	if !f.Patched() {
		pin, err := currentForkPin(f)
		if err != nil {
			return "", err
		}
		commit = pin.Commit
	}
	return preparationForkIdentity(f, commit, d.Runtime, d.Platform)
}

func ordinaryCaptureDelivery(r npmCaptureRecord) (entrypoint.ProgramCaptureDelivery, error) {
	if r.Resolution == nil {
		return entrypoint.ProgramCaptureDelivery{}, fmt.Errorf("ordinary preparation completed without resolved facts — retry a fresh launch")
	}
	inputs, err := r.Resolution.inputs()
	if err != nil {
		return entrypoint.ProgramCaptureDelivery{}, err
	}
	identity, err := inputs.Identity()
	if err != nil {
		return entrypoint.ProgramCaptureDelivery{}, err
	}
	if identity != r.Identity {
		return entrypoint.ProgramCaptureDelivery{}, errPreparationAdmissionStale
	}
	return entrypoint.ProgramCaptureDelivery{Declared: inputs.Declared, Compatibility: identity, Platform: inputs.Platform, Runtime: inputs.Runtime, NodeFloor: r.Resolution.Input.NodeFloor}, nil
}

func waitOrdinaryPreparation(ctx context.Context, in npmCaptureInput, fingerprint, rt, platform string, fresh bool, w io.Writer, foregroundGuard func(func() error) error) (npmCaptureRecord, error) {
	store := &capture.Store{Dir: paths.CapturesDir()}
	probe := preparationDemand{Key: ordinaryProgramKey(in), Kind: preparationOrdinaryProgramKind, Runtime: rt, Platform: platform}
	current, currentFingerprint, release, err := resolveOrdinaryPreparation(ctx, probe)
	release()
	foreground := config.InJail() || errors.Is(err, errSharedPreparationForegroundOnly) || err == nil && (current == nil || *current != in || currentFingerprint != fingerprint)
	if err != nil && !foreground {
		return npmCaptureRecord{}, err
	}
	// admitted is the generation this machine already prepared for exactly these inputs, kept only
	// when its last check FAILED and PD-NF1 has this launch retry it (admittedServes).
	var admitted *npmCaptureRecord
	var recovery admittedRecovery
	if !fresh {
		record, readErr := readNPMCaptureRecord(store, in)
		if readErr == nil && record.Fingerprint == fingerprint && record.Resolution != nil && record.Resolution.Runtime.Platform == platform {
			entry, _, hitErr := resolveProgramCaptureFor(store, in.Bin, platform, record.Identity)
			if hitErr == nil {
				manifest, manifestErr := capture.ReadManifest(entry.Root)
				if manifestErr == nil {
					manifestErr = validateProgramManifest(manifest, platform, npmCaptureArtifacts(in, record.Identity))
				}
				if manifestErr != nil {
					return record, manifestErr
				}
				if record.Failure != "" {
					recovery = jailAdmittedRecovery(in)
				}
				if record.Failure == "" || admittedServes(in, record, recovery, w) {
					if err = foregroundGuard(func() error { return ctx.Err() }); err != nil {
						return record, err
					}
					return record, nil
				}
				// A newer version was found and preparing it failed: this launch retries it in
				// front of the program, as the next launch after a failed background refresh does.
				admitted, fresh = &record, true
			}
		}
	}
	d := preparationDemand{Key: ordinaryProgramKey(in), Kind: preparationOrdinaryProgramKind, Runtime: rt, Platform: platform, Identity: ordinaryProgramIdentity(in, fingerprint, rt, platform), Fresh: fresh}
	record, err := waitOrdinaryCheck(ctx, store, in, fingerprint, d, foreground, w, foregroundGuard)
	if err == nil || admitted == nil || ctx.Err() != nil || errors.Is(err, errNPMCaptureUnsupported) {
		return record, err
	}
	return retriedAdmitted(ctx, store, in, *admitted, err, recovery, w, foregroundGuard)
}

// waitOrdinaryCheck registers this launch's request and runs the check d asks for, in the
// foreground or through the shared worker.
func waitOrdinaryCheck(ctx context.Context, store *capture.Store, in npmCaptureInput, fingerprint string, d preparationDemand, foreground bool, w io.Writer, foregroundGuard func(func() error) error) (npmCaptureRecord, error) {
	fresh := d.Fresh
	boundary, err := registerNPMCaptureRequest(ctx, store, in)
	if err != nil {
		return npmCaptureRecord{}, err
	}
	d.AfterSeq = boundary
	if foreground {
		fmt.Fprintf(w, "%s: foreground-only installation preparation; the user resolver does not authorize these exact inputs for a detached worker\n", in.Bin)
		return advanceNPMCapture(withOrdinaryCaptureRuntime(ctx, d.Runtime), store, in, fingerprint, fresh, boundary, w, foregroundGuard)
	}
	result, err := waitSharedDemandResult(ctx, d, false, func(pid int) {
		fmt.Fprintf(w, "%s: waiting for shared ordinary preparation (pid %d); interrupting detaches this requester\n", in.Bin, pid)
	})
	if err != nil {
		return npmCaptureRecord{}, err
	}
	if ctx.Err() != nil {
		return npmCaptureRecord{}, ctx.Err()
	}
	if failure := result.ordinaryFloorFailure(in); failure != nil {
		return npmCaptureRecord{}, failure
	}
	if result.UnsupportedNPM {
		return npmCaptureRecord{}, fmt.Errorf("%w: %s", errNPMCaptureUnsupported, result.OperationError)
	}
	if result.Failed || result.OperationError != "" {
		return npmCaptureRecord{}, fmt.Errorf("ordinary preparation failed: %s — retry a fresh launch", result.OperationError)
	}
	if result.Program == nil {
		return npmCaptureRecord{}, fmt.Errorf("ordinary worker returned no admitted generation — retry preparation")
	}
	if err = foregroundGuard(func() error { return ctx.Err() }); err != nil {
		return npmCaptureRecord{}, err
	}
	if fresh && result.Program.Completed <= boundary {
		return npmCaptureRecord{}, fmt.Errorf("ordinary check predates the request — retry --update")
	}
	return *result.Program, nil
}

// admittedRecovery is what PD-NF1's decision over an admitted generation needs from the act that
// serves it — a jail launch (jailAdmittedRecovery) or the host floor (wireFloorOrdinaryCapture) —
// so both decide by the one rule (admittedServes, retriedAdmitted).
type admittedRecovery struct {
	// Held is an `agent_updates` hold on the program's pack; UseInstalled is the user's
	// YOLO_USE_INSTALLED naming the program.
	Held, UseInstalled bool
	// Prefix starts each line said ("yolo host: " on the host floor, "" at a jail launch).
	Prefix string
	// Repair is the command that retries the preparation; Command the one the hatch is put in
	// front of (hostfloor.UpdateRefusal's fields of the same names).
	Repair, Command string
}

// jailAdmittedRecovery is a jail launch's: the user-scope `agent_updates` policy, and `yolo` to
// retry and to put the hatch in front of.
func jailAdmittedRecovery(in npmCaptureInput) admittedRecovery {
	return admittedRecovery{Held: !entrypoint.PackPolicyAllows(config.AgentUpdatesWire(), in.Owner),
		UseInstalled: ordinaryUseInstalled(in), Repair: "yolo", Command: "yolo"}
}

// admittedServes is PD-NF1 for an admitted generation whose last check failed (record.Failure):
// true when that generation runs at this launch without a check, with the one line saying why. An
// `agent_updates` hold runs it unchecked; a check that failed before it found anything is the
// could-not-check case; and YOLO_USE_INSTALLED naming the program is the user's own hatch. Anything
// else is a newer version yolo found and could not prepare, which this launch retries (false).
func admittedServes(in npmCaptureInput, record npmCaptureRecord, rc admittedRecovery, w io.Writer) bool {
	old := record.Resolution.Release
	switch {
	case rc.Held:
		return true
	case record.FailedRelease == "":
		fmt.Fprintf(w, "%s%s: could not check for a newer version (%s); running the prepared %s\n", rc.Prefix, in.Bin, firstLine(record.Failure), old)
		return true
	case rc.UseInstalled:
		fmt.Fprintf(w, "%s%s: preparing %s failed (%s); %s is set, so yolo runs the prepared %s\n",
			rc.Prefix, in.Bin, record.FailedRelease, firstLine(record.Failure), patchrecovery.UseInstalledEnv, old)
		return true
	}
	return false
}

// retriedAdmitted is the outcome of a retried check that failed with err, over the admitted
// generation: a newer version found again is PD-NF1's refusal; a check that failed before it found
// one is the could-not-check case, and the admitted generation runs.
func retriedAdmitted(ctx context.Context, store *capture.Store, in npmCaptureInput, admitted npmCaptureRecord, err error, rc admittedRecovery, w io.Writer, foregroundGuard func(func() error) error) (npmCaptureRecord, error) {
	old := admitted.Resolution.Release
	newer := admitted.FailedRelease
	if after, readErr := readNPMCaptureRecord(store, in); readErr == nil {
		if after.FailedRelease == "" {
			fmt.Fprintf(w, "%s%s: could not check for a newer version (%s); running the prepared %s\n", rc.Prefix, in.Bin, firstLine(err.Error()), old)
			if gerr := foregroundGuard(func() error { return ctx.Err() }); gerr != nil {
				return admitted, gerr
			}
			return admitted, nil
		}
		newer = after.FailedRelease
	}
	return admitted, &hostfloor.UpdateRefusal{Bin: in.Bin, Pack: in.Owner, Installed: old, Newer: newer,
		Act: "preparing the newer version", Err: err, Repair: rc.Repair, Command: rc.Command, Runs: "run the prepared " + old}
}

// ordinaryUseInstalled reports whether YOLO_USE_INSTALLED covers in: "1", or a name equal to its
// pack, its bin or its npm package (PD-NF2's grammar).
func ordinaryUseInstalled(in npmCaptureInput) bool {
	scope := patchrecovery.Read(os.Getenv, patchrecovery.UseInstalledEnv)
	if scope.Covers(patchrecovery.Subject{Key: in.Owner + "/" + in.Bin, Pack: in.Owner, Name: in.Bin}) {
		return true
	}
	name, _, err := npmCaptureSpec(in.Package)
	return err == nil && scope.Covers(patchrecovery.Subject{Name: name})
}

// ordinaryPreparationFailed is a selected program whose preparation failed with no admitted
// generation to fall back to, or under an explicit --update: the launch stops, in PD-NF1's refusal
// shape. It offers no hatch: nothing older is prepared for these inputs (or --update asked for the
// check), and YOLO_ALLOW_MISSING_PROGRAMS is not read on this path, which stops before the jail's
// readiness act could honor it.
type ordinaryPreparationFailed struct {
	Bin string
	Err error
}

func (e *ordinaryPreparationFailed) Error() string {
	return fmt.Sprintf("preparing %s failed: %v", e.Bin, e.Err)
}

func (e *ordinaryPreparationFailed) Unwrap() error { return e.Err }

// Render is the refusal block: the ERROR line naming the program, the facts, a blank line, then
// the repair.
func (e *ordinaryPreparationFailed) Render(color bool) string {
	var b strings.Builder
	head := fmt.Sprintf("ERROR: %s: preparing it failed", e.Bin)
	b.WriteString(richtext.Render("[bold red]"+richtext.Escape(head)+"[/bold red]", color) + "\n")
	for _, l := range strings.Split(strings.TrimRight(fmt.Sprint(e.Err), "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("\n" + richtext.Render("[bold]Repair:[/bold]", color) + "\n")
	b.WriteString("  fix what the error names, then retry the launch:\n")
	b.WriteString("    yolo\n")
	return b.String()
}

// ordinaryLaunchFailure is err as the launch reports it for in: PD-NF1's refusal and the Node
// floor's typed refusal pass through, an interrupt stays an interrupt, and anything else is the
// nothing-admitted refusal naming in.
func ordinaryLaunchFailure(in npmCaptureInput, err error) error {
	var refusal *hostfloor.UpdateRefusal
	var floor *run.NodeFloorUnavailable
	if errors.As(err, &refusal) || errors.As(err, &floor) || errors.Is(err, context.Canceled) {
		return err
	}
	return &ordinaryPreparationFailed{Bin: in.Bin, Err: err}
}

func prepareOrdinaryForLaunch(req run.ProgramCaptureRequest, w io.Writer) (map[string]entrypoint.ProgramCaptureDelivery, error) {
	if len(req.PrivateClients) > 0 && (req.UnsupportedRuntime != "" || req.PrivateClientEntrypoints == nil) {
		cause := "its delivery is unsupported here: " + req.UnsupportedRuntime
		if req.UnsupportedRuntime == "" {
			cause = "this launch passed no place for its checked delivery"
		}
		return nil, &privateClientDeliveryRefusal{Cell: req.PrivateClients[0], Cause: cause, Repair: privateClientBaselineRepair}
	}
	if req.UnsupportedRuntime != "" {
		fmt.Fprintf(w, "ordinary artifact reuse is unsupported here: %s; retaining the authorized bounded installers — use a baseline runtime without that customization for sealed reuse\n", req.UnsupportedRuntime)
		return map[string]entrypoint.ProgramCaptureDelivery{}, nil
	}
	mode := entrypoint.ProgramInstallOnly
	if req.Fresh {
		mode = entrypoint.ProgramFreshRefresh
	}
	vector, err := selectedOrdinaryInputs(req.Env, req.Packs, mode, req.Platform)
	if err != nil {
		return nil, err
	}
	deliveries := map[string]entrypoint.ProgramCaptureDelivery{}
	// A selected private HTTP client's delivery is checked here, and its unsupported or
	// image-provided outcomes refuse rather than fall back (verifyPrivateClientDelivery).
	private := map[string]packload.PrivateProgramEnvPlan{}
	for _, cell := range req.PrivateClients {
		private[cell.Bin] = cell
	}
	vector = skipImageProvidedPrograms(vector, req.ImageIdentity, &capture.Store{Dir: paths.CapturesDir()}, w)
	if len(vector) == 0 {
		return deliveries, nil
	}
	var operationErr error
	req.Act.Scope(func(ctx context.Context) {
		if err := registerOrdinaryPreparations(ctx, vector, req, w); err != nil {
			fmt.Fprintf(w, "ordinary timer preparation could not start (%v) — this launch still prepares its required artifacts; run a fresh launch to retry the timer\n", err)
		}
		// No unaudited private client is prepared at all: it is refused before its recipe resolves.
		for _, in := range vector {
			if cell, isPrivate := private[in.Bin]; isPrivate {
				if err := refuseUnauditedPrivateClient(cell, in); err != nil {
					operationErr = err
					return
				}
			}
		}
		for _, in := range vector {
			guard := foregroundOrdinaryGuard(ctx, req, in)
			record, err := waitOrdinaryPreparation(ctx, in, req.Fingerprint, req.Runtime, req.Platform, req.Fresh, w, guard)
			if cell, isPrivate := private[in.Bin]; isPrivate && (npmCaptureImageProvided(err) || errors.Is(err, errNPMCaptureUnsupported)) {
				operationErr = &privateClientDeliveryRefusal{Cell: cell, Cause: "its delivery is unsupported: " + err.Error(), Repair: privateClientBaselineRepair}
				return
			}
			if npmCaptureImageProvided(err) {
				fmt.Fprintf(w, "%s: the jail image already provides it, so nothing is prepared and the image's copy runs — drop the pack's `program %s` declaration to skip this check\n", in.Bin, in.Bin)
				continue
			}
			if errors.Is(err, errNPMCaptureUnsupported) {
				fmt.Fprintf(w, "%s: sealed dependency capture is unsupported (%v); retaining its bounded declared installer — use an entirely locked registry dependency graph for sealed reuse\n", in.Bin, err)
				continue
			}
			if err != nil {
				operationErr = ordinaryLaunchFailure(in, err)
				return
			}
			delivery, err := ordinaryCaptureDelivery(record)
			if err != nil {
				operationErr = err
				return
			}
			if cell, isPrivate := private[in.Bin]; isPrivate {
				checked, err := verifyPrivateClientDelivery(cell, in, record, delivery, req.Platform, req.Packs)
				if err != nil {
					operationErr = err
					return
				}
				req.PrivateClientEntrypoints[in.Bin] = checked
			}
			deliveries[in.Bin] = delivery
		}
	})
	if req.Act.Interrupted() {
		return nil, context.Canceled
	}
	if operationErr != nil {
		return nil, operationErr
	}
	return deliveries, nil
}

func foregroundOrdinaryGuard(ctx context.Context, req run.ProgramCaptureRequest, in npmCaptureInput) func(func() error) error {
	return func(commit func() error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Foreground authority is the launch's live selection. Re-read its config and
		// runtime recipe rather than converting unresolved workspace inputs into a daemon.
		cfg, err := config.LoadConfig(req.Workspace, true, nil)
		if err != nil {
			return err
		}
		if run.OrdinaryRuntimeUnsupported(cfg, os.Getenv("YOLO_STORE_PACKAGES") == "1") != "" {
			return errPreparationAdmissionStale
		}
		fingerprint, err := run.ProgramRuntimeFingerprint(req.RepoRoot, cfg)
		if err != nil || fingerprint != req.Fingerprint {
			return errPreparationAdmissionStale
		}
		resolver, release := preparationScopedResolver(ctx)
		defer release()
		selected := selectHostPacks(resolver, config.UserScopeSelection())
		if len(selected.problems()) > 0 {
			return errPreparationAdmissionStale
		}
		vector, err := selectedOrdinaryInputs(ordinarySelectionEnvironment(cfg, selected.packs), selected.packs, entrypoint.ProgramInstallOnly, req.Platform)
		if err != nil {
			return err
		}
		for _, current := range vector {
			if current == in {
				return commit()
			}
		}
		return errPreparationAdmissionStale
	}
}

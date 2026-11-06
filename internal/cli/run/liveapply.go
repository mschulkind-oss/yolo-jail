package run

// liveapply.go is `yolo config apply` and the attach-time gate
// (docs/design/live-config-apply.md §3, §6, §8): the launch's own config-change gate taken to a
// running jail, then each approved change the jail can take applied in place, and every other
// one reported with the step that will apply it.
//
// THE ORDER IS THE SEMANTICS (LA-P2, LA-D12): the approval is recorded first, by the launch's
// gate (runConfigGate, the body of checkConfigChanges); then each live-apply step runs on its
// own, with its own deadline, and a step that fails leaves the trust captured and the change
// pending. Nothing is rolled back across steps.
//
// SEAMS. The live-apply steps reach the broker's scope files (brokerscope) and the mount
// injector (ctxinjectdrive.go, ctxmountrecord.go) through the package-level variables below, so
// a test can drive every outcome without a running jail or a broker.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// liveScopeFile is one live broker's scope file of this jail: lane B's brokerscope.LiveFile,
// with its LiveScope capability (LA-D11) lifted out.
type liveScopeFile struct {
	Path      string
	File      brokerscope.File
	LiveScope bool
}

// liveCtxMount is one config mount the host's record says the jail has.
type liveCtxMount = ctxMountEntry

// ctxMountsTokenEnvName is the jail-env variable carrying the token its mount record is
// written with.
const ctxMountsTokenEnvName = ctxMountsTokenEnv

var (
	liveScopeFilesSeam = func(ctx context.Context, source, container, workspace string) ([]liveScopeFile, error) {
		files, err := brokerscope.LiveFilesContext(ctx, source, container, workspace)
		out := make([]liveScopeFile, 0, len(files))
		for _, f := range files {
			out = append(out, liveScopeFile{Path: f.Path, File: f.File, LiveScope: f.File.LiveScope})
		}
		return out, err
	}
	replaceScopeReposSeam         = brokerscope.ReplaceScopeContext
	invalidateCtxMountRecordSeam  = invalidateCtxMountRecord
	ctxMountsLiveSeam             = (*Options).ctxMountsLive
	ctxMountRecordInvalidatedSeam = ctxMountRecordInvalidated
	readCtxMountRecordSeam        = readCtxMountRecord
	// The token is validated against the inspected jail env before mount planning. After the
	// record is tombstoned, restore only that token and only while the tombstone is still there.
	writeCtxMountRecordSeam = func(cname, token string, entries []liveCtxMount) error {
		return updateCtxMountRecord(cname, token, entries)
	}
	diffCtxMountsSeam  = diffCtxMounts
	injectCtxMountSeam = (*Options).injectCtxMount
	removeCtxMountSeam = (*Options).removeCtxMount
)

// applyStepDeadline and applyBudget are §8's defaults: each live step gets 10 s, and the
// whole apply 60 s after the y. A step past either is at next restart, with the reason.
var (
	applyStepDeadline = 10 * time.Second
	applyBudget       = 60 * time.Second
)

// configApplyUsage is `yolo config apply --help`.
const configApplyUsage = `Usage: yolo config apply [--record-only] [--accept-config-changes]

Approve this workspace's config changes on the host, and apply to its running jail
what the jail can take without a restart. It asks the question a fresh launch asks
(the same diff, the same repository scope block, the same y/N, the same approval
record), then says, per change, whether it was applied now, reaches your next yolo
session, or waits for the next restart (yolo stop, then yolo). It never restarts
the jail. With no jail running it records the approval, and the next yolo launches
with the changes. HOST ONLY: in a jail it refuses.

Flags:
  --record-only            Approve and record; touch no running jail.
  --accept-config-changes  Approve without a terminal, as on a launch.
  --help, -h               Show this help.

Exit: 0 nothing pending, 3 approved with a restart still owed, 1 declined,
refused or failed before the approval was recorded, 2 misuse.`

// ConfigApply is `yolo config apply`, registered in internal/cli's config verbs. args is
// everything after `apply`; color is the config command's one color decision.
func ConfigApply(args []string, stdout, stderr io.Writer, color bool) int {
	recordOnly, accept := false, false
	for _, a := range args {
		switch a {
		case "--help", "-h", "help":
			_, _ = io.WriteString(stdout, configApplyUsage+"\n")
			return 0
		case "--record-only":
			recordOnly = true
		case config.AcceptConfigChangesFlag:
			accept = true
		default:
			fmt.Fprintf(stderr, "yolo config apply: unknown argument %q\n\n%s\n", a, configApplyUsage)
			return 2
		}
	}
	opts := NewDefaultOptions()
	opts.Color = color
	opts.Stdout, opts.Stderr = stdout, stderr
	opts.AcceptConfigChanges = accept
	fillDefaults(&opts)
	return opts.configApply(recordOnly)
}

// applyMode is how much of the sequence an apply may run.
type applyMode struct {
	// running: this workspace's jail is known to be running (or, on macos-user, sessions
	// may be). false with recordOnly "" is "known not running".
	running bool
	// recordOnly, when set, is why nothing is applied: --record-only, or a runtime that
	// could not be asked. The approval is still recorded.
	recordOnly string
	// unknown: the runtime could not say whether the jail runs, so nothing is folded.
	unknown bool
	// attach: an attach's gate, whose N starts the session anyway (LA-D14).
	attach bool
}

// configApply is the command's sequence (design §3.2): refuse in a jail, load and validate the
// config, resolve the runtime, hold the launch lock for the whole run, probe the jail
// tri-state, then the gate and the apply.
func (o *Options) configApply(recordOnly bool) int {
	// THE JAIL'S OWN WORKSPACE IS REFUSED, not every in-jail run (LA-D17): approving this jail's
	// config from inside it is the side being approved approving itself (OQ-S2). A workspace a
	// launch inside the jail opens is a nested jail, whose launch already runs its gate in here,
	// so its apply is no wider than that launch.
	if o.inJail() && config.IsJailOwnWorkspace(o.Workspace) {
		out := o.pr(o.Stderr)
		out.print("[bold red]yolo config apply runs on the host, not in this jail: recording an approval " +
			"must not be reachable from the side being approved.[/bold red]")
		out.print("[dim]Ask the user to run `yolo config apply` on the host, in this workspace. " +
			"It shows them the change and applies what the running jail can take.[/dim]")
		return 1
	}
	cfg, ok := o.loadAndValidateConfig()
	if !ok {
		return 1
	}
	rt, ok := o.resolveRuntime(cfg)
	if !ok {
		return 1
	}
	cname := o.jailName()
	// THE LAUNCH LOCK, prompt included (LA-D3): a launch or attach that arrives meanwhile
	// waits with the existing line, and this apply never interleaves with one.
	o.holdLaunchLock(cname)
	defer o.releaseLaunchLock()

	mode := applyMode{running: true}
	var envLines []string
	if recordOnly {
		mode.recordOnly = "recorded only (--record-only)"
	}
	if rt != "macos-user" { // parity: HonoredBy — macos-user has no container to probe; its live sessions are found by their broker files
		cid, known, failure := o.probeJailForApply(cname, rt)
		switch {
		case !known:
			mode.running, mode.unknown = false, true
			mode.recordOnly = "could not ask " + rt + " whether this jail is running"
			out := o.pr(o.Stderr)
			out.printf("[yellow]Could not ask %s whether this workspace's jail (%s) is running: %s[/yellow]",
				rt, cname, richtext.Escape(termsafe.Visible(failure)))
			out.printf("[dim]Recording the approval only. Run `%s` to see why, then run "+
				"`yolo config apply` again to apply what the jail can take.[/dim]", runningListCommand(rt))
		case cid == "":
			mode.running = false
			o.prOut(o.Stdout).print("[cyan]No jail is running for this workspace: recording the approval " +
				"only, and the next `yolo` here launches with these changes.[/cyan]")
		default:
			envLines, _ = o.inspectContainerEnv(rt, cname)
		}
	}
	return o.liveApply(cfg, rt, cname, envLines, mode)
}

// probeJailForApply is the launch's tri-state probe (probeRunningContainer), with the
// runtime's own words kept for the "could not ask" line.
func (o *Options) probeJailForApply(cname, rt string) (cid string, known bool, failure string) {
	var last ExecResult
	var lastArgv []string
	exec := o.Exec
	o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		r := exec(argv, dir, env, timeout)
		last, lastArgv = r, argv
		return r
	}
	cid, known = o.probeRunningContainer(cname, rt, attachProbeTimeout)
	o.Exec = exec
	if !known {
		failure = runtimeProbeFailure(strings.Join(lastArgv, " "), last)
	}
	return cid, known, failure
}

// applyRun is one apply's inputs, kept from the plan to the steps.
type applyRun struct {
	cfg      *jsonx.OrderedMap
	rt       string
	cname    string
	envLines []string
	mode     applyMode
	current  *jsonx.OrderedMap
	have     []liveCtxMount
}

// liveApply is §3.2's steps 3 to 8, which an interactive attach runs too: the plan, the
// launch's gate with the plan printed above its question, the record (the gate's), each live
// step, the fold into the effective-config record, the result, and the exit.
func (o *Options) liveApply(cfg *jsonx.OrderedMap, rt, cname string, envLines []string, mode applyMode) int {
	run := &applyRun{cfg: cfg, rt: rt, cname: cname, envLines: envLines, mode: mode}
	var plan []applyChange
	moved := false
	hooks := &gateHooks{attach: mode.attach, apply: !mode.attach, plan: func(read *config.WorkspaceRead, scope *config.ScopeCheck) []string {
		// THE PLAN IS OF THE CONFIG THE GATE APPROVES (B1). cfg was loaded before the gate read
		// the workspace again, and an agent can rewrite the file between the two: every step
		// below is derived from cfg, so it runs only when the gate's read composes to cfg.
		if !o.gateReadIsLoaded(cfg, read) {
			moved = true
			return nil
		}
		run.current = read.Config
		plan = o.applyPlan(run, scope)
		if len(plan) == 0 {
			return nil
		}
		return append([]string{"[bold]If you answer y, each change:[/bold]"}, o.applyBlock(plan, rt)...)
	}}
	if !o.runConfigGate(cfg, rt, hooks) {
		if mode.attach {
			return 0
		}
		return 1
	}
	if moved {
		out := o.pr(o.Stderr)
		out.print("[bold red]Nothing was applied: the workspace config changed while yolo was reading it.[/bold red]")
		if mode.attach {
			out.print("[dim]This session starts with the jail as it is. Run `yolo config apply` again " +
				"on the host to apply the config as it is now.[/dim]")
			return 0
		}
		out.print("[dim]Run `yolo config apply` again to see and apply the config as it is now.[/dim]")
		return 1
	}
	if len(plan) == 0 {
		if !mode.attach {
			o.prOut(o.Stdout).print("[green]Nothing to apply: the jail already has this workspace's config.[/green]")
		}
		return 0
	}
	result := o.runApplySteps(run, plan)
	// THE RESULT IS `yolo config apply`'s PAYLOAD, so that verb prints it on stdout (prOut); an
	// attach runs this sequence inside a launch, whose stdout is the session's, so there it is a
	// launch line on stderr like every other (launchlog.go).
	out := o.prOut(o.Stdout)
	if mode.attach {
		out = o.pr(o.Stderr)
	}
	out.print("[bold]Config apply result:[/bold]")
	for _, line := range o.applyBlock(result, rt) {
		out.print(line)
	}
	rc := 0
	for _, c := range result {
		if c.outcome.pending() {
			rc = 3
		}
	}
	if rc == 3 && mode.running && !mode.unknown {
		out.print("[dim]A restart ends every session of this jail, so yolo never does it for you: when " +
			"they are done, run `yolo stop`, then `yolo`. The approval is recorded, so the launch asks " +
			"nothing.[/dim]")
	}
	return rc
}

// applyPlan gathers the plan's inputs (applyPlanInput) and builds it. scope is the gate's own
// scope check; nothing here writes.
func (o *Options) applyPlan(run *applyRun, scope *config.ScopeCheck) []applyChange {
	current := run.current
	if current == nil {
		current = jsonx.NewOrderedMap()
		run.current = current
	}
	boot, bootOK, _ := config.ReadWorkspaceBootBaseline(o.Workspace, o.setupName())
	in := applyPlanInput{rt: run.rt, running: run.mode.running || run.mode.unknown, boot: boot,
		bootOK: bootOK, current: current, mountRefusal: liveMountRefusal,
		sessionInputs: jailTakesSessionInputs(run.envLines)}
	if run.rt == "podman" && run.mode.running { // parity: Warned — only Podman supports live mounts; other backends report why they wait
		in.mountRecordInvalidated = ctxMountRecordInvalidatedSeam(run.cname, run.envLines)
	}
	if run.rt == "podman" && run.mode.running { // parity: Warned — live mounts are podman only; elsewhere the mounts line says why it waits
		in.mountsLive, in.mountsReason = ctxMountsLiveSeam(o, run.rt, run.cname, run.envLines)
		if have, ok := readCtxMountRecordSeam(run.cname, run.envLines); ok {
			run.have = have
			in.mountRecordOK = true
			in.mountAdds, in.mountRemoves = diffCtxMountsSeam(have, o.configCtxMounts(run.rt, run.cfg, nil))
		}
	}
	// NO RECORD, READ-WRITE MOUNTS: those come from the user scope, which the workspace config
	// and its record never carry, so nothing else can say whether they moved (M3).
	if run.mode.running && !run.mode.unknown && !in.mountRecordOK {
		in.userMountsUnknown = userScopeMountsUnknown(run.rt, o.configCtxMounts(run.rt, run.cfg, nil))
	}
	if scope != nil {
		changedLabels := map[string]bool{}
		if r, err := config.ScopeChanges(o.Workspace, scope); err == nil {
			for _, l := range r.ScopeLabels {
				changedLabels[l] = true
			}
		}
		discoveryCtx, cancelDiscovery := context.WithTimeout(context.Background(), applyStepDeadline)
		defer cancelDiscovery()
		for _, s := range scope.Sources {
			d := scopeDelta{label: s.Label, source: s.Source, desired: s.Repos(),
				desiredGrants: s.Grants, recordChanged: changedLabels[s.Label]}
			if run.mode.running && run.rt != "container" { // parity: Warned — Apple Container starts no broker; the scope line says so
				files, discoveryErr := liveScopeFilesSeam(discoveryCtx, s.Source, run.cname, o.Workspace)
				d.files = files
				if discoveryErr != nil {
					d.discoveryErr = discoveryErr.Error()
					d.files = nil
				}
			}
			in.scopes = append(in.scopes, d)
		}
		in.grants = o.grantDeltas(discoveryCtx, run, scope)
	}
	plan := buildApplyPlan(in)
	if run.mode.recordOnly != "" {
		for i := range plan {
			if plan[i].outcome == outcomeAppliedNow {
				plan[i].outcome, plan[i].reason = outcomeNextRestart, run.mode.recordOnly
			}
		}
	}
	return plan
}

// gateReadIsLoaded reports whether the gate's read of the workspace config, composed as a launch
// composes it, is cfg, the config this apply loaded and validated before the gate read again.
// Anything it cannot prove is false.
func (o *Options) gateReadIsLoaded(cfg *jsonx.OrderedMap, read *config.WorkspaceRead) bool {
	if cfg == nil || read == nil {
		return false
	}
	composed, err := config.ComposeWithWorkspaceRead(o.Workspace, read.Config)
	if err != nil {
		return false
	}
	a, err1 := jsonx.DumpsSnapshot(cfg)
	b, err2 := jsonx.DumpsSnapshot(composed)
	return err1 == nil && err2 == nil && a == b
}

// applyBlock renders the outcome block: one line per change, colored by outcome, each with
// the launch disclosure it carries.
func (o *Options) applyBlock(plan []applyChange, rt string) []string {
	var lines []string
	for _, c := range plan {
		color := "yellow"
		switch c.outcome {
		case outcomeAppliedNow:
			color = "green"
		case outcomeNextSession:
			color = "cyan"
		}
		lines = append(lines, "  ["+color+"]"+richtext.Escape(termsafe.Visible(c.line()))+"[/"+color+"]")
		if c.disclosure && c.kind == changeMountAdd {
			lines = append(lines, "    "+rwMountDisclosure(c.mountAdd, o.rootfulPodmanHost(rt)))
		}
	}
	return lines
}

// runApplySteps runs each applied-now change's step in plan order, under its deadline and the
// apply's budget, and folds each key whose every change took effect. It returns the plan with
// each change's outcome as it ended.
func (o *Options) runApplySteps(run *applyRun, plan []applyChange) []applyChange {
	res := append([]applyChange(nil), plan...)
	budgetEnd := time.Now().Add(applyBudget)
	stopped := false
	mountRecordUnavailable := false
	token := envLineValue(run.envLines, ctxMountsTokenEnvName)
	have := append([]liveCtxMount(nil), run.have...)
	for i := range res {
		c := &res[i]
		if c.outcome != outcomeAppliedNow || c.kind == changeKey {
			continue
		}
		if stopped {
			c.outcome, c.reason = outcomeNextLaunch, "jail stopped; applies at next launch"
			continue
		}
		if mountRecordUnavailable && (c.kind == changeMountAdd || c.kind == changeMountRemove) {
			c.outcome, c.reason = outcomeNextRestart, "the host's mount record is unavailable; further mount changes wait for restart"
			continue
		}
		left := time.Until(budgetEnd)
		if left <= 0 {
			c.outcome, c.reason = outcomeNextRestart, fmt.Sprintf("the apply's %s budget ran out", applyBudget)
			continue
		}
		if c.kind == changeMountAdd || c.kind == changeMountRemove {
			// Make the host's record non-authoritative before a mutation. If the injector or the
			// following record write fails, a later apply cannot trust the previous mount set.
			if err := invalidateCtxMountRecordSeam(run.cname); err != nil {
				c.outcome, c.reason = outcomeNextRestart,
					"could not invalidate the live-mount record before changing mounts: "+err.Error()
				mountRecordUnavailable = true
				continue
			}
			mountRecordUnavailable = true
		}
		// THE DEADLINE GOES DOWN TO EVERY CALL THE STEP MAKES, rather than a wait abandoning a
		// step that keeps running: one cut off that way could mount after the record below was
		// written without it.
		limit := min(applyStepDeadline, left)
		err := o.applyStep(run, c, time.Now().Add(limit))
		if err != nil {
			if run.rt != "macos-user" { // parity: HonoredBy — macos-user's steps are scope files, not a container that can stop
				if cid, known := o.probeRunningContainer(run.cname, run.rt, attachProbeTimeout); known && cid == "" {
					stopped = true
					c.outcome, c.reason = outcomeNextLaunch, "jail stopped; applies at next launch"
					continue
				}
			}
			c.outcome, c.reason = outcomeNextRestart, err.Error()
			continue
		}
		switch c.kind {
		case changeMountAdd:
			have = append(have, liveCtxMount{Source: c.mountAdd.source, Dest: c.mountAdd.dest, RW: c.mountAdd.rw})
			if !o.noteMountRecord(run, token, have, c) {
				mountRecordUnavailable = true
			} else {
				mountRecordUnavailable = false
			}
		case changeMountRemove:
			kept := have[:0:0]
			for _, h := range have {
				if h != c.mountRemove {
					kept = append(kept, h)
				}
			}
			have = kept
			if !o.noteMountRecord(run, token, have, c) {
				mountRecordUnavailable = true
			} else {
				mountRecordUnavailable = false
			}
		}
	}
	folded := o.foldApplied(run, res)
	o.updateMechanicsAfterApply(run, res, folded)
	return res
}

// noteMountRecord rewrites the host's record of the jail's mounts after one step. If that
// write fails, the injected mount still exists, but its state is unknown to the next apply: the
// stale record is invalidated, this change stays pending, and no later mount step runs.
func (o *Options) noteMountRecord(run *applyRun, token string, have []liveCtxMount, c *applyChange) bool {
	if err := writeCtxMountRecordSeam(run.cname, token, have); err == nil {
		return true
	} else {
		invalidateErr := invalidateCtxMountRecordSeam(run.cname)
		c.outcome = outcomeNextRestart
		c.reason = "the mount succeeded, but its host record could not be updated: " + err.Error()
		if invalidateErr != nil {
			c.reason += "; the stale record could not be invalidated: " + invalidateErr.Error()
		}
		return false
	}
}

func contextForDeadline(deadline time.Time) (context.Context, context.CancelFunc) {
	if deadline.IsZero() {
		return context.WithCancel(context.Background())
	}
	return context.WithDeadline(context.Background(), deadline)
}

// applyStep is one change's live-apply step, every call it makes ending by deadline. A scope step
// rewrites local files only, which no deadline interrupts.
func (o *Options) applyStep(run *applyRun, c *applyChange, deadline time.Time) error {
	switch c.kind {
	case changeMountAdd:
		return injectCtxMountSeam(o, run.rt, run.cname, c.mountAdd, deadline)
	case changeMountRemove:
		return removeCtxMountSeam(o, run.rt, run.cname, c.mountRemove, deadline)
	case changeScope:
		// THE SCOPE THE GATE APPROVED, its in-memory result (WW-P2), never a re-read.
		approved, ok := o.approvedScopes[c.scope.label]
		if !ok {
			return errors.New("no repository scope was approved for " + c.scope.label + " in this apply")
		}
		repos := make([]string, len(approved))
		for i, r := range approved {
			repos[i] = r.Repo
		}
		// The grants go with the repositories, from the same gate (docs/design/git-broker-plan.md
		// ID-10).
		grants := o.approvedGrants[c.scope.label]
		ctx, cancel := contextForDeadline(deadline)
		defer cancel()
		for _, f := range c.scope.files {
			if err := replaceScopeReposSeam(ctx, f.Path, f.File, repos, grants); err != nil {
				return err
			}
		}
		return nil
	case changeGrants:
		// THE GRANTS THE GATE APPROVED, its in-memory result (WW-P2), never a re-read.
		ctx, cancel := contextForDeadline(deadline)
		defer cancel()
		return o.replaceGrantFiles(ctx, c)
	}
	return nil
}

type foldedAppliedKey struct {
	key     string
	value   any
	present bool
}

// foldApplied folds into the effective-config record every key whose every change took effect
// (LA-D8): applied now, or reaching the next session. Container backends only, and only for a
// jail known running; macos-user writes none (each session carries its own baseline digest).
// The returned values are the exact per-key producer results whose baseline writes succeeded;
// consumers must not infer approval from the desired config or a timing outcome alone.
func (o *Options) foldApplied(run *applyRun, res []applyChange) []foldedAppliedKey {
	if run.rt == "macos-user" || !run.mode.running || run.mode.unknown || run.current == nil { // parity: HonoredBy — macos-user writes no record: each session carries its own baseline digest
		return nil
	}
	ok := map[string]bool{}
	var order []string
	for _, c := range res {
		if c.key == "" {
			continue
		}
		if _, seen := ok[c.key]; !seen {
			ok[c.key] = true
			order = append(order, c.key)
		}
		if c.outcome != outcomeAppliedNow && c.outcome != outcomeNextSession {
			ok[c.key] = false
		}
	}
	var folded []foldedAppliedKey
	for _, k := range order {
		if !ok[k] {
			continue
		}
		value, present, err := config.ConfigValueAtPath(run.current, k)
		if err != nil {
			o.pr(o.Stderr).printf("[dim]Warning: could not read applied config path %s before folding it into %s: %s; `yolo config apply` can retry it.[/dim]",
				k, config.WorkspaceConfigBootPath(o.Workspace, o.setupName()), richtext.Escape(err.Error()))
			continue
		}
		if err := config.FoldBootBaselineKey(o.Workspace, o.setupName(), k, run.current); err != nil {
			o.pr(o.Stderr).printf("[dim]Warning: could not record %s as applied in %s: %s; `yolo config "+
				"drift` will still list it.[/dim]", k, config.WorkspaceConfigBootPath(o.Workspace, o.setupName()),
				richtext.Escape(err.Error()))
			continue
		}
		if present {
			value = jsonx.DeepCopy(value)
		}
		folded = append(folded, foldedAppliedKey{key: k, value: value, present: present})
	}
	return folded
}

// attachPendingNotice is the one line a non-interactive attach prints when the workspace config
// moved past what the jail has or what was approved (LA-D9).
const attachPendingNotice = "This workspace's config has changes this jail does not have; nothing was " +
	"asked or applied for this session. Run `yolo config apply` on the host to approve and apply " +
	"them, or `yolo config drift` to see what differs."

// applyAtAttach is the attach-time gate (design §3.3), called by attachExisting with the launch
// lock held, after the session is counted and before the briefings are refreshed. A human is
// there when stdin is a terminal, the launch gate's own predicate (LA-D13), or the attach
// carried the launch's --accept-config-changes: then it is the command's sequence, and an N
// starts the session anyway. Otherwise it asks nothing and prints one notice when something is
// pending. Nothing here can fail the attach (LA-D14).
func (o *Options) applyAtAttach(cname, rt string, cfg *jsonx.OrderedMap, envLines []string) {
	if rt == "macos-user" { // parity: NotApplicable — macos-user has no attach: every invocation is a fresh launch, which runs the gate
		return
	}
	if o.IsTTYStdin() || o.AcceptConfigChanges {
		o.liveApply(cfg, rt, cname, envLines, applyMode{running: true, attach: true})
		return
	}
	if o.configChangesPending(rt, cname, cfg, envLines) {
		o.pr(o.Stderr).print("[yellow]" + attachPendingNotice + "[/yellow]")
	}
}

// configChangesPending is the non-interactive attach's cheap check, with no side effect: the
// effective-config record drifted from the workspace config, or the approval record differs
// from it, config part or scope part, or the host's record of the jail's mounts differs from the
// mounts the config now binds (a read-write one is the user scope's, which neither record holds).
func (o *Options) configChangesPending(rt, cname string, cfg *jsonx.OrderedMap, envLines []string) bool {
	if _, drifted, _, err := config.WorkspaceConfigDrift(o.Workspace, o.setupName()); err == nil && drifted {
		return true
	}
	if rt == "podman" { // parity: NotApplicable — only a podman jail has the host's record of its mounts
		if ctxMountRecordInvalidatedSeam(cname, envLines) {
			return true
		}
		if have, ok := readCtxMountRecordSeam(cname, envLines); ok {
			// No gate runs here: the writable requests compared are the ones the approval record
			// already holds (RC-D24), so an unapproved one reads as a pending change.
			o.workspaceMounts = config.ApprovedWorkspaceMounts(o.Workspace)
			if add, rm := diffCtxMountsSeam(have, o.configCtxMounts(rt, cfg, nil)); len(add) > 0 || len(rm) > 0 {
				return true
			}
		}
	}
	read, err := config.ReadWorkspaceForGate(o.Workspace)
	if err != nil {
		return false
	}
	if changed, err := config.ConfigPartChanged(o.Workspace, read.Config); err == nil && changed {
		return true
	}
	lps := o.brokeredToStart(rt, cfg)
	if len(lps) == 0 {
		return false
	}
	brokers := make([]config.ScopeBroker, 0, len(lps))
	for _, lp := range lps {
		brokers = append(brokers, config.ScopeBroker{Source: lp.Brokered.Source, Label: lp.Name,
			RemoteHost: lp.Brokered.RemoteHost, GrantKinds: lp.Brokered.Grants})
	}
	r, err := config.ScopeChanges(o.Workspace, config.NewScopeCheck(o.Workspace, brokers, read))
	return err == nil && r.ScopeChanged
}

// userScopeMountsUnknown reports whether a running jail with no readable mount record may lack a
// read-write mount the user scope lists. PODMAN ONLY: its record is what would vouch, and a
// launch writes one, so a restart clears the line. Apple Container never writes a record and
// binds its shares when its VM boots, so a line there would stand on every apply and attach,
// restarts included; macos-user has no mounts, only links each session re-stages.
func userScopeMountsUnknown(rt string, mounts []configCtxMount) bool {
	if rt != "podman" { // parity: Warned — only podman keeps the record a read-write mount change is read from
		return false
	}
	for _, m := range mounts {
		if m.rw {
			return true
		}
	}
	return false
}

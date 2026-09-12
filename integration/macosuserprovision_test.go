package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// RUNBOOK ITEM 7 — the provisioning stage runs, and is confined.
// docs/plans/runbooks/macos-user-manual-checks.md §7, written 2026-09-12 and never run.
//
// WHAT ONLY A MAC CAN SETTLE HERE, and what is already settled elsewhere. The stage's
// COMPOSITION is unit-pinned and needs no Mac: internal/macosuser/provision_test.go
// asserts the argv carries /usr/bin/sandbox-exec and does NOT carry `sudo --login`, and
// PlanInvariants refuses a plan that violates either. So this file deliberately does not
// re-assert the argv. What no unit test can reach is whether that composed argv RUNS —
// design §10.8 items 1-4, all four of which this one launch settles at once:
//
//  1. sandbox-exec ACCEPTS the stage process (a rejected profile never runs the script,
//     so nothing below would exist at all);
//  2. the CONFINED stage reaches the NETWORK (`mise install` of a real tool resolves and
//     downloads from inside the Seatbelt profile);
//  3. it can WRITE into the workspace sidecar (the startup log is a file it creates as
//     _yolojail under a directory the invoking user owns);
//  4. `sudo --user=… /usr/bin/env -i … sandbox-exec …` forwards the script VERBATIM.
//
// ⚠ ITEM 4 IS THE ONE TO WATCH, and it is why the banner's TIMESTAMP is parsed rather
// than merely eyeballed. `sudo --login` does not execve its argv — it concatenates and
// backslash-escapes everything except alphanumerics, underscores, hyphens and DOLLAR
// SIGNS, then feeds the result to a login shell (measured on hardware 2026-09-11,
// internal/macosuser/provision.go states both reproductions). The stage script is dense
// with `$`: "$_prc", "${PIPESTATUS[0]}", and the `$(date …)` that produces this very
// banner. Under that mangling the stage does not ERROR — it provisions nothing and exits
// 0. The evidence is therefore the log's CONTENT and never the exit code, exactly as the
// runbook says: a banner carrying a parseable timestamp from inside this launch's window
// is proof that an inner shell expanded `$(date …)` at the right moment, and a mangled
// script cannot produce one.
//
// THE FAILURE PATH IS ITS OWN TEST — a stage whose body fails must not abort the launch
// (runbook item 8) — in macosuserstagefailure_test.go. This one is the success path only.
func TestMacosUserProvisioningStageRunsAndRecordsItself(t *testing.T) {
	requireMacosUser(t)

	// The runbook's own workspace, spelled its way: "a workspace whose config declares one
	// cheap tool, e.g. {"mise_tools": {"jq": "latest"}}". `mise_tools` is one of the exactly
	// two keys ProvisionNeeded counts (internal/macosuser/provision.go), so this is also the
	// minimum config that makes the stage exist at all.
	ws := macosUserWorkspace(t, `{"mise_tools": {"jq": "latest"}}`)
	sentinel := macosUserSeedStageLog(t, ws)

	start := time.Now()
	r := runMacosUser(t, ws, `echo "=== AGENT ==="`)
	elapsed := time.Since(start)

	if r.rc != 0 {
		t.Fatalf("runbook item 7: the macos-user launch failed (rc %d), so there is no "+
			"stage to judge.\n\nIf the floor is what failed, item 6 is the bug and this "+
			"is one of its consequences — run TestMacosUserFloorReachesTheSandboxPath "+
			"first and report only that.\n\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "=== AGENT ===") {
		t.Errorf("runbook item 7: the launch exited 0 but the agent never ran — nothing "+
			"printed the probe marker. The stage sits between the bootstrap and the agent, "+
			"so a stage that swallowed the launch looks exactly like this.\n"+
			"stdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}

	logPath := provision.StartupLog(ws)
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("runbook item 7: no startup log at %s: %v\n\nThe stage either never "+
			"started (sandbox-exec rejected the profile, or sudo refused — §10.8 item 1) "+
			"or could not write into the workspace sidecar as %s (§10.8 item 3). The "+
			"launch's own output is the place to tell those apart.\nstdout:\n%s\nstderr:\n%s",
			logPath, err, macosuser.SandboxUser, r.stdout, r.stderr)
	}
	log := string(body)

	// TRUNCATED TO THIS LAUNCH. The sentinel was written before the launch and no byte of
	// it may survive, because the briefing reader (jailcontent.ReadProvisioningFailed)
	// greps this file for the failure marker and would otherwise report a previous
	// launch's failure as this one's. TWO mechanisms clear it — the CLI removes the file
	// before starting the stage (runProvisionStage, so "this launch's log" is a fact
	// rather than a hope) and the script truncates it one instruction later — and this
	// asserts the PROPERTY both exist for rather than either mechanism.
	if strings.Contains(log, sentinel) {
		t.Errorf("runbook item 7: the startup log still carries the sentinel this test "+
			"wrote BEFORE the launch, so the log was appended to rather than replaced. "+
			"The next launch's briefing greps this file for %q and would attribute a "+
			"stale failure to a healthy launch.\nlog %s:\n%s",
			provision.FailedMarker, logPath, log)
	}

	// THE BANNER, and the three separate facts its first line carries: that the log begins
	// with this launch's record, that `$(date …)` was expanded by the shell that ran the
	// script (§10.8 item 4), and — through the timestamp's VALUE — that the expansion
	// happened during this launch and not during some earlier one.
	first, _, _ := strings.Cut(log, "\n")
	ts, ok := macosUserStageBannerTime(first)
	if !ok {
		t.Fatalf("runbook item 7: the startup log's first line is not a provisioning "+
			"banner.\n  got:  %q\n  want: %q followed by a %q timestamp and %q\n\n"+
			"A first line missing its timestamp is the SIGNATURE OF A MANGLED SCRIPT: "+
			"`sudo --login` would have expanded `$(date …)` against an empty environment "+
			"before the inner shell saw it, and such a stage provisions nothing while "+
			"exiting 0. Compare the stage argv in `yolo --dry-run` against "+
			"internal/macosuser/provision.go's ProvisionArgv.\nlog %s:\n%s",
			first, macosUserStageBannerPrefix, macosUserStageBannerLayout,
			macosUserStageBannerSuffix, logPath, log)
	}
	// Two seconds of slack for the second-granularity timestamp and for the clock read
	// landing either side of `start`; the window this rules out is "a previous launch",
	// which is minutes or days away, never seconds.
	if ts.Before(start.Add(-2 * time.Second)) {
		t.Errorf("runbook item 7: the banner is dated %s but this launch began at %s — "+
			"the log predates the launch, so it belongs to an earlier one and this "+
			"launch's stage wrote nothing.\nlog %s:\n%s",
			ts.Format(time.RFC3339), start.Format(time.RFC3339), logPath, log)
	}

	// THE STEPS, in the order THIS BACKEND declares them. The list is read out of
	// macosuser.ProvisionSetup rather than written here, so a change to which of
	// provision's steps macos-user takes moves this assertion with it — and the
	// announce TEXT is read out of the step constants for the same reason. Both
	// derivations are pinned on Linux, in `just test-fast`, by the tests at the foot of
	// this file; a derived list that quietly became empty would make everything below
	// vacuous, which is what TestMacosUserStageSubsetIsWhatItemSevenReads rules out.
	announces := macosUserStageAnnounces(macosuser.ProvisionSetup(
		macosuser.ProvisionBootstrapScript(ws)))
	if len(announces) == 0 {
		t.Fatalf("runbook item 7: macos-user's stage declares no announce steps, so this "+
			"test has nothing to look for in %s and every check below would pass "+
			"vacuously.", logPath)
	}
	for i, want := range announces {
		if strings.Contains(log, want) {
			continue
		}
		// The steps are `&&`-joined (provision.Setup), so the FIRST announce missing from
		// the log is where the stage stopped, and what failed is whatever ran just before
		// it — the previous step, or the stage's own startup when nothing ran at all.
		blame := "the stage never reached its first step: it started (the banner is above) " +
			"and died before any step ran"
		if i > 0 {
			blame = "the step announced by " + announces[i-1] + " failed, so the `&&` " +
				"chain stopped there"
		}
		t.Errorf("runbook item 7: the startup log never records %q — %s.\n\n"+
			"For `mise install` the usual cause is the CONFINED stage not reaching the "+
			"NETWORK (§10.8 item 2), which the Seatbelt profile is what would deny.\n"+
			"log %s:\n%s", want, blame, logPath, log)
		break
	}
	if strings.Contains(log, provision.FailedMarker) {
		t.Errorf("runbook item 7: the stage recorded %q. It RAN — so §10.8 items 1, 3 and "+
			"4 are settled and only its BODY failed — and the log below names the step "+
			"that did it. Three causes account for nearly all of them: no network inside "+
			"the profile (§10.8 item 2), a tool `mise` cannot resolve, and the generated "+
			"bootstrap script's own npm work failing under Seatbelt.\nlog %s:\n%s",
			provision.FailedMarker, logPath, log)
	}

	// THE TEE REACHED THE HUMAN. The log is only half the record: the stage's output is
	// piped through `tee -a` to the launch's own stderr, and a stage whose progress landed
	// in a file nobody opens is one the human watching the launch cannot see stall.
	if !strings.Contains(r.combined(), announces[0]) {
		t.Errorf("runbook item 7: %q is in the startup log but never reached the launch's "+
			"console. The stage's output is tee'd to stderr (provision.Script), so a human "+
			"watching a slow first launch sees nothing at all.\nstdout:\n%s\nstderr:\n%s",
			announces[0], r.stdout, r.stderr)
	}

	// ⚠ THE MEASUREMENT the runbook asks for, and §10.8 item 5 says nobody has ever taken:
	// what a first launch costs. It is a t.Logf and not an assertion on purpose — there is
	// no budget to compare against yet, and inventing one here would turn the first real
	// number into a failure. The split is free: the banner is written as the stage's first
	// instruction, so everything before it is the floor build, the staging and the
	// bootstrap, and everything after it is the stage itself (the agent is one `echo`).
	// Clamped at zero: the banner's timestamp has second granularity, so a stage that
	// started in the same second the launch did can round to just before it.
	preStage := max(ts.Sub(start), 0)
	t.Logf("runbook item 7 timing — launch total %s; before the stage (nix floor + "+
		"staging + bootstrap) %s; stage + agent %s. macosUserTimeout() is %s, and it is a "+
		"ceiling on waste rather than a budget: revise it from numbers like these.",
		elapsed.Round(time.Second), preStage.Round(time.Second),
		(elapsed - preStage).Round(time.Second), macosUserTimeout())

	// ⚠ THE WORKING DIRECTORY — the runbook's second warning on this item, and §10.8 item
	// 9 of the design doc. Both ask for a launch "from outside the workspace tree", on the
	// theory that the stage argv sets no cwd so a workspace-local mise.toml may go unread.
	//
	// THAT LAUNCH IS NOT EXPRESSIBLE, and the source is unambiguous about why: the
	// workspace IS the working directory — internal/cli/run/runcmd.go sets
	// `o.Workspace = wd` from os.Getwd(), with no walk-up, no flag and no host-side env
	// override (YOLO_WORKSPACE is a jail-side input, read by internal/config/load.go
	// inside the jail). Running from outside the tree therefore does not launch THIS
	// workspace from elsewhere; it launches a different workspace. The stage then inherits
	// that same cwd, because runReal sets no cmd.Dir and neither `sudo` without --login
	// nor `env -i` changes directory — so the stage always runs in the workspace it is
	// provisioning, and a workspace-local mise.toml is read.
	//
	// Logged rather than asserted because every probe that would PROVE the stage's cwd
	// works by making mise fail on a file only that cwd can see, and a failing stage is
	// the one thing this test exists to rule out. The failing-stage launch next door is
	// where such a probe would belong if the derivation above ever comes into doubt.
	t.Logf("runbook item 7 cwd note: the stage ran with the launcher's cwd, which IS the "+
		"workspace (%s) because yolo has no way to name a workspace other than by being "+
		"in it. The runbook's second warning, and §10.8 item 9, describe a launch that "+
		"cannot be spelled.", ws)
}

// macosUserSeedStageLog writes a sentinel into the workspace's startup log BEFORE a
// launch, and returns it, so the caller can assert no byte of it survives.
//
// It creates the sidecar directory exactly the way a launch does — host-side, as the
// invoking user: attachLaunchLog (internal/cli/run/launchlog.go) MkdirAll's
// <workspace>/.yolo at the top of Run, above the macos-user branch, so every backend gets
// it from the same place. Seeding therefore adds no state a launch would not have made.
//
// The grant is then re-applied, because the ACL the shared root hands out is INHERITED AT
// CREATION: a directory this test makes after macosUserWorkspace already ran
// `macos-fix-permissions` is covered only if the grant is applied again. Without it the
// stage would fail to write its log as the sandbox user, and that failure would read as
// the defect item 7 is hunting rather than as the fixture.
func macosUserSeedStageLog(t *testing.T, ws string) string {
	t.Helper()
	logPath := provision.StartupLog(ws)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("creating the workspace sidecar %s: %v", filepath.Dir(logPath), err)
	}
	const sentinel = "yolo-integration-sentinel: a PREVIOUS launch wrote this line"
	if err := os.WriteFile(logPath, []byte(sentinel+"\n"), 0o644); err != nil {
		t.Fatalf("seeding %s: %v", logPath, err)
	}
	if r := runCommand(t, ws, []string{"macos-fix-permissions", ws}); r.rc != 0 {
		t.Fatalf("`yolo macos-fix-permissions %s` failed (rc %d) after this test created "+
			"the sidecar directory. The stage writes its log there as %s and cannot "+
			"without the grant:\n%s", ws, r.rc, macosuser.SandboxUser, r.combined())
	}
	return sentinel
}

// The banner provision.Script writes as the stage's first instruction:
//
//	printf "=== yolo provisioning %s ===\n" "$(date "+%Y-%m-%dT%H:%M:%S%z")"
//
// The layout is that strftime format in Go's reference time. Spelled here rather than
// derived from the shell format string because there is no mechanical translation between
// the two dialects; TestMacosUserStageBannerMatchesTheScriptItParses keeps the pair
// honest against the production script on every `just test-fast`.
const (
	macosUserStageBannerPrefix = "=== yolo provisioning "
	macosUserStageBannerSuffix = " ==="
	macosUserStageBannerLayout = "2006-01-02T15:04:05-0700"
)

// macosUserStageBannerTime parses the stage log's first line, reporting the instant the
// stage started and whether the line was a banner at all.
func macosUserStageBannerTime(line string) (time.Time, bool) {
	s := strings.TrimSuffix(strings.TrimSpace(line), macosUserStageBannerSuffix)
	stamp, ok := strings.CutPrefix(s, macosUserStageBannerPrefix)
	if !ok {
		return time.Time{}, false
	}
	ts, err := time.Parse(macosUserStageBannerLayout, strings.TrimSpace(stamp))
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// macosUserStageAnnounceSteps is every provision step that only ANNOUNCES. Item 7 reads
// the startup log for these, because they are also what a tee'd log shows a reader as the
// last thing that STARTED — the step that failed is always the one they precede.
//
// Written down because there is nothing in a step's TYPE that says it is an announce; it
// is a string like every other. TestMacosUserStageSubsetIsWhatItemSevenReads fails if
// macos-user's stage ever takes an announce step that is not in this list, which is the
// half a hand-written list normally lacks.
var macosUserStageAnnounceSteps = []string{
	provision.StepAnnounceMiseInstall,
	provision.StepAnnounceBootstrap,
}

// macosUserStageAnnounces returns the TEXT of every announce step in a stage body, in the
// order the stage runs them. `setup` is a provision.Setup result — steps joined with
// " && " — so splitting on that separator recovers the members in order.
func macosUserStageAnnounces(setup string) []string {
	var out []string
	for _, step := range strings.Split(setup, " && ") {
		for _, announce := range macosUserStageAnnounceSteps {
			if step == announce {
				out = append(out, macosUserAnnounceText(step))
			}
		}
	}
	return out
}

// macosUserAnnounceText extracts the text a provision.StepAnnounce* constant echoes —
// `echo "  ↳ mise install" >&2` yields `  ↳ mise install`.
//
// Derived rather than transcribed so that rewording a step is a change in ONE place. A
// transcribed copy would keep compiling, keep passing everywhere this suite is developed,
// and fail only on the Mac that finally runs it, with a diff nobody there can explain.
func macosUserAnnounceText(step string) string {
	i := strings.Index(step, `"`)
	j := strings.LastIndex(step, `"`)
	if i < 0 || j <= i {
		return step
	}
	return step[i+1 : j]
}

// ---------------------------------------------------------------------------
// The two helpers above read the PRODUCTION script, and these two pin what they read.
// They run on Linux under -short, in `just test-fast`, for the reason the macos-user gate
// exists at all: a Mac-only assertion whose expected value drifted is a red nobody on the
// Mac can explain, and this repo's own machines are where the drift happens.
// ---------------------------------------------------------------------------

// The announce lines are what the startup log shows a reader as the last thing that
// STARTED, so item 7 reads them to say how far the stage got. This pins the extraction
// against the real constants: reword a step and this fails here, on Linux, in the
// pre-commit gate — not on the Mac three weeks later.
func TestMacosUserStageAnnounceTextIsWhatTheStageEchoes(t *testing.T) {
	for _, tc := range []struct{ step, want string }{
		{provision.StepAnnounceMiseInstall, "  ↳ mise install"},
		{provision.StepAnnounceBootstrap, "  ↳ bootstrap"},
	} {
		if got := macosUserAnnounceText(tc.step); got != tc.want {
			t.Errorf("macosUserAnnounceText(%q) = %q, want %q. Runbook item 7 greps the "+
				"startup log for this text; if the step was deliberately reworded, update "+
				"the want here and nothing else — the integration assertion reads the "+
				"constant.", tc.step, got, tc.want)
		}
	}
}

// The banner is the evidence that the stage script reached the sandbox UNMANGLED (§10.8
// item 4), so item 7 parses it rather than eyeballing it. Its two halves are pinned
// differently on purpose: the literal text is compared against the real script, and the
// timestamp LAYOUT against the strftime format the script hands `date`, because there is
// no mechanical translation between Go's reference time and strftime.
func TestMacosUserStageBannerMatchesTheScriptItParses(t *testing.T) {
	script := provision.Script("/Users/Shared/yolo/ws/.yolo/startup.log", "true")
	for _, want := range []string{
		macosUserStageBannerPrefix,
		macosUserStageBannerSuffix,
		// The strftime format macosUserStageBannerLayout mirrors, character for
		// character: %Y-%m-%dT%H:%M:%S%z ⇄ 2006-01-02T15:04:05-0700.
		`+%Y-%m-%dT%H:%M:%S%z`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the provisioning script no longer contains %q, so runbook item 7's "+
				"banner parser is reading a line the stage does not write any more. Either "+
				"the banner changed (update the constants in this file) or it was dropped "+
				"(item 7 loses its only proof that the script reached the sandbox "+
				"verbatim).\nscript:\n%s", want, script)
		}
	}
	// The parser accepts a banner shaped like the one that script produces...
	const stamp = "2026-09-12T10:30:00-0400"
	ts, ok := macosUserStageBannerTime(macosUserStageBannerPrefix + stamp + macosUserStageBannerSuffix)
	if !ok {
		t.Fatalf("macosUserStageBannerTime rejected a well-formed banner for %s", stamp)
	}
	if got := ts.Format(macosUserStageBannerLayout); got != stamp {
		t.Errorf("banner round-trip: parsed %s back as %s", stamp, got)
	}
	// ...and rejects the shapes a MANGLED script produces, which is the half that matters:
	// `sudo --login` eats `$(date …)` and leaves an empty or unexpanded stamp behind, and
	// item 7 must read that as a failure rather than as a banner it could not quite parse.
	for _, bad := range []string{
		macosUserStageBannerPrefix + macosUserStageBannerSuffix,                   // $(date …) expanded to nothing
		macosUserStageBannerPrefix + `$(date "+%Y")` + macosUserStageBannerSuffix, // never expanded
		"  ↳ mise install", // the log starts mid-stage
		"",
	} {
		if _, ok := macosUserStageBannerTime(bad); ok {
			t.Errorf("macosUserStageBannerTime accepted %q as a banner. Item 7 would then "+
				"read a mangled or truncated log as a healthy one.", bad)
		}
	}
}

// Pins the DERIVATION item 7 rests on: that macos-user's stage announces exactly the steps
// this file knows how to read, in the order it expects them.
//
// BOTH DIRECTIONS ARE CHECKED, and they fail differently. Too FEW — the subset drops a
// step — and item 7 silently stops checking that half of the stage while staying green.
// Too MANY — the subset gains an announce step nobody added here — and item 7 keeps
// passing without ever asking whether the new step ran. The second is the quiet one, which
// is why this walks the subset rather than only looking for the names it already knows.
//
// It runs on Linux, under -short, in `just test-fast`. Adding a step to
// macosuser.ProvisionSetup is then a one-line addition here plus a moment's thought about
// what item 7 should say when the new step is the one that failed — which is the whole
// point of being told at all.
func TestMacosUserStageSubsetIsWhatItemSevenReads(t *testing.T) {
	setup := macosuser.ProvisionSetup("/Users/Shared/yolo/ws/.yolo/yolo-bootstrap.sh")

	var announced []string
	for _, step := range strings.Split(setup, " && ") {
		if strings.HasPrefix(step, `echo "`) {
			announced = append(announced, step)
		}
	}
	if len(announced) != len(macosUserStageAnnounceSteps) {
		t.Fatalf("macos-user's stage announces %d step(s); runbook item 7 knows how to "+
			"read %d.\n  stage: %q\n  item 7: %q\nA stage with FEWER leaves item 7 green "+
			"while checking less than it says; a stage with MORE leaves the new step "+
			"unchecked. Reconcile macosUserStageAnnounceSteps with "+
			"macosuser.ProvisionSetup.", len(announced), len(macosUserStageAnnounceSteps),
			announced, macosUserStageAnnounceSteps)
	}
	for i, step := range announced {
		if step != macosUserStageAnnounceSteps[i] {
			t.Errorf("stage announce step %d is %q; runbook item 7 expects %q there. The "+
				"ORDER is load-bearing: item 7 reports the first missing announce as the "+
				"point the `&&` chain stopped, and blames the step before it.",
				i, step, macosUserStageAnnounceSteps[i])
		}
	}
	if got := macosUserStageAnnounces(setup); len(got) != len(announced) {
		t.Fatalf("macosUserStageAnnounces recovered %d of %d announce steps from the "+
			"stage body — item 7's checks would be that much weaker.", len(got), len(announced))
	} else {
		t.Logf("item 7 reads these announce steps out of the startup log, in stage "+
			"order: %q", got)
	}
}

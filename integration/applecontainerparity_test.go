package integration

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE UNRUN APPLE CONTAINER FIXES AND ONE UNANSWERED QUESTION, ASKED ON THE HARDWARE AS
// EXPERIMENTS — the second batch of the exception applecontainer_test.go's header names.
//
// WHAT IS HERE. docs/design/backend-parity.md §5 lists the macOS parity fixes shipped on
// 2026-08-24. On Apple Container only fix #1 has a direct hardware test
// (TestAppleContainerMachineWideTierArrives); the others were unit-tested and mutation-checked
// and have never run on a Mac. This file asks each one's remaining question — the part an argv
// cannot show — on the self-hosted runner apple-container.yml dispatches to:
//
//	#2  TestAppleContainerRescuesAStrandedSharedDir        the rescued bytes reach the jail THROUGH the machine mount
//	#3  TestAppleContainerReadsHostGrantArrives            the materialized reads-host copy is composed into the surface
//	#4  TestAppleContainerHostFilesSourceArrivesUnmasked   a host_files FILE source arrives with its bytes, not an empty 0o444 file
//	#8  TestAppleContainerBriefingAdvertisesNoLoopholes    the briefing that ARRIVES lists no loophole the backend never started
//	#10 TestAppleContainerExplicitHostModeKeepsPublishedPorts  an explicit `network.mode: host` still publishes `network.ports`
//
// plus OQ-WP5 (docs/design/workspace-path-mirroring.md), TestAppleContainerBindsADeepDestination.
//
// # Both answers PASS. Only a run that failed to conduct the experiment is red
//
// That is TestAppleContainerReachesHostLoopback's rule, and it is the only way a test can
// arrive in this suite unrun without making the job red on day one for a fact about the
// hardware. Each test records its answer on one line, prefixed acParityTag with the fix number
// (`AC-PARITY #3 VERDICT: HOLDS — …`), so one grep of the `go test -v` job log recovers the
// whole record. What IS a failure: the launch or the control run not happening, or a probe
// that produced nothing to read — every one of those means nothing was measured.
//
// # THE PROMOTION RULE
//
// A recorded HOLDS is the evidence the file header asks for. The commit that cites it turns
// that test's DOES-NOT-HOLD branch into a t.Errorf and moves the test out of this file's list
// into the header's set of checks, so a regression after that is red. A recorded DOES NOT
// HOLD is a defect in shipped code: file it against the fix in backend-parity.md §5, with the
// log line.
//
// THE FIXES THAT HAVE NO TEST HERE, and why: #1 is already a check; #5 is pinned at argv level
// on macos-user (launchflagsdispatch_test.go) and is not an Apple Container fix; #7 on this
// backend is a launch LINE, which an argv test sees (backendwarns_test.go); #11 and #13 became
// the acROBindsFloor version gate after TestAppleContainerHonorsReadOnlyBinds measured the
// premise inverted; #12's premise was refuted by TestAppleContainerBindsASingleFile. The rest
// are macos-user fixes (macosuserparity_test.go).

// acParityTag prefixes every verdict line this file logs.
const acParityTag = "AC-PARITY"

// acParityNonce is a per-run token, so a marker an earlier run left behind can never answer
// for this one.
func acParityNonce() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

// acParityRecord logs one experiment's answer. Both answers pass; see the file header.
func acParityRecord(t *testing.T, fix string, holds bool, finding, evidence string) {
	t.Helper()
	verdict := "HOLDS"
	if !holds {
		verdict = "DOES NOT HOLD"
	}
	t.Logf("%s %s VERDICT: %s — %s\n\nevidence:\n%s", acParityTag, fix, verdict, finding, evidence)
}

// acParityRun launches script on Apple Container and FAILS the test when the launch did not
// run it — the experiment-not-conducted case. extra is appended to the launcher's env.
func acParityRun(t *testing.T, fix, dir, script string, extra ...string) result {
	t.Helper()
	opts := []runOption{appleContainerEnv()}
	if len(extra) > 0 {
		opts = append(opts, withEnv(extra...))
	}
	res := runYolo(t, dir, script, opts...)
	if res.rc != 0 || !strings.Contains(res.stdout, "=== END ===") {
		t.Fatalf("%s %s: the launch did not run its probe (rc=%d), so NOTHING WAS MEASURED — "+
			"this is not an answer about the fix.\nstdout:\n%s\nstderr:\n%s",
			acParityTag, fix, res.rc, lastLines(res.stdout, 60), lastLines(res.stderr, 60))
	}
	return res
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// OQ-WP5: does Apple Container support an arbitrary deep bind destination?
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestAppleContainerBindsADeepDestination answers OQ-WP5
// (docs/design/workspace-path-mirroring.md#OQ-WP5): can Apple Container bind a directory at a
// deep destination the image does not have? Workspace path mirroring's option A would mount the
// workspace at its host path (`/Users/<you>/code/<proj>`) instead of `/workspace`, and move the
// per-side shadows with it, so a *yes* there needs this answered. It does not block the doc's
// current *no*.
//
// Three shapes, because each is a different ask of the runtime:
//
//   - a brand-new TOP-LEVEL tree (`/Users/…/proj`) — what option A produces on a Mac;
//   - a destination nested several levels inside another read-write bind — where the per-side
//     shadows would land under a mirrored workspace, and the shape the suite's GlobalCache
//     mount already takes one level deep;
//   - a deep new path under a directory the image already has (`/opt/…`).
//
// Each runs with `--read-only`, because the launch's own argv carries it (assemble.go's
// runFlags), and a shape that fails is re-run without it, so the record says whether the
// DESTINATION or the read-only rootfs is what refused. The nested case also records whether the
// runtime created the mountpoint on the HOST side, inside the parent bind's source: podman does,
// and a mirrored workspace would then gain empty directories on the user's disk.
//
// IT DOES NOT GO THROUGH yolo, for TestAppleContainerHonorsReadOnlyBinds' reasons: the question
// is about the runtime, and a throwaway temp dir is the only safe subject. A control run of a
// shallow bind — the shape TestAppleContainerBindsASingleFile already runs green — must succeed,
// or nothing was measured.
func TestAppleContainerBindsADeepDestination(t *testing.T) {
	requireAppleContainer(t)
	requireJail(t)

	ref := imageExists("container")
	if ref == "" {
		t.Skip("no jail image is loaded into Apple Container yet, and this probe needs one " +
			"to run anything at all; the launching tests above are what load it")
	}
	ver, _ := exec.Command("container", "--version").Output()
	version := strings.TrimSpace(string(ver))

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	outer := filepath.Join(dir, "outer")
	for _, d := range []string{src, outer} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	nonce := "WP5-" + acParityNonce()
	if err := os.WriteFile(filepath.Join(src, "seed"), []byte(nonce+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), jailTimeout())
		defer cancel()
		out, err := exec.CommandContext(ctx, "container", append([]string{"run", "--rm"}, args...)...).
			CombinedOutput()
		return string(out), err
	}

	if out, err := run("-v", src+":/wp5-control", ref, "sh", "-c", "cat /wp5-control/seed"); err != nil ||
		!strings.Contains(out, nonce) {
		t.Fatalf("AC-WP5: the CONTROL — a shallow directory bind, the shape the suite already runs "+
			"green — did not deliver its seed (err=%v), so nothing below could be measured.\n"+
			"version: %s\n%s", err, version, out)
	}

	type deepCase struct{ name, why, dest string }
	cases := []deepCase{
		{"new-top-level", "the mirrored workspace path option A would produce on a Mac: a " +
			"top-level tree the image does not have", "/Users/yolo-wp5-probe/code/proj"},
		{"nested-in-a-bind", "four levels inside another read-write bind, where the per-side " +
			"shadows land under a mirrored workspace", "/workspace/a/b/c/proj"},
		{"deep-under-an-image-dir", "a deep new path under a directory the image already has",
			"/opt/yolo-wp5/a/b/c/proj"},
	}
	var record strings.Builder
	var unsupported []string
	for _, c := range cases {
		script := fmt.Sprintf("cat %s/seed 2>&1; touch %s/written-%s 2>&1 && echo WRITE-OK || echo WRITE-FAILED",
			c.dest, c.dest, c.name)
		binds := []string{"-v", src + ":" + c.dest}
		if c.name == "nested-in-a-bind" {
			binds = append([]string{"-v", outer + ":/workspace"}, binds...)
		}
		attempt := func(readOnly bool) (arrived, wrote bool, out string, err error) {
			args := []string{}
			if readOnly {
				args = append(args, "--read-only")
			}
			args = append(append(args, binds...), ref, "sh", "-c", script)
			out, err = run(args...)
			_, statErr := os.Stat(filepath.Join(src, "written-"+c.name))
			return strings.Contains(out, nonce), statErr == nil, out, err
		}
		arrived, wrote, out, err := attempt(true)
		outcome := fmt.Sprintf("ARRIVED with --read-only (host saw the in-container write: %v)", wrote)
		if !arrived {
			unsupported = append(unsupported, c.name)
			a2, w2, out2, err2 := attempt(false)
			outcome = fmt.Sprintf("NOT ARRIVED with --read-only (err=%v); without --read-only: "+
				"arrived=%v wrote=%v (err=%v)\n    --read-only output: %s\n    plain output: %s",
				err, a2, w2, err2, strings.TrimSpace(out), strings.TrimSpace(out2))
		}
		if c.name == "nested-in-a-bind" {
			_, stErr := os.Stat(filepath.Join(outer, "a", "b", "c", "proj"))
			outcome += fmt.Sprintf("; mountpoint created on the HOST inside the parent bind: %v", stErr == nil)
		}
		fmt.Fprintf(&record, "  %-24s %s\n    why: %s\n    → %s\n", c.name, c.dest, c.why, outcome)
	}

	if len(unsupported) == 0 {
		t.Logf("AC-WP5 VERDICT: SUPPORTED — Apple Container binds a directory at every deep "+
			"destination shape asked (`container` %s).\n%s\n"+
			"OQ-WP5's answer is yes: nothing about Apple Container blocks a mirrored workspace "+
			"destination. Record it in docs/design/workspace-path-mirroring.md#OQ-WP5.", version, record.String())
		return
	}
	t.Logf("AC-WP5 VERDICT: NOT SUPPORTED for %v (`container` %s).\n%s\n"+
		"OQ-WP5's answer is no for those shapes: a mirrored workspace destination (option A) has "+
		"no spelling on this backend there. The per-case lines say whether the read-only rootfs "+
		"or the destination itself is what refused. Record it in "+
		"docs/design/workspace-path-mirroring.md#OQ-WP5.", unsupported, version, record.String())
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// backend-parity.md §5 #2: the stranded machine-wide copy is rescued, and seen.
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestAppleContainerRescuesAStrandedSharedDir asks fix #2 (`db2e096c`) on the hardware.
//
// Before fix #1 this backend mounted no `scope: machine` pack dir, so its whole-home wsState
// bind is where one's contents landed — an Apple Container user's
// ~/.claude-shared-credentials sits in <workspace>/.yolo/home. Fix #1 mounts the dir from
// GlobalHome, which SHADOWS that stranded copy; fix #2 copies it into GlobalHome, if-missing,
// before the launch (prepareWsState). Linux tests pin the copy; what they cannot show is the
// jail reading the rescued bytes through the NESTED machine mount rather than through the
// wsState bind beneath it.
//
// So there are two probes, and the second is what tells the two binds apart: a file only the
// STRANDED copy has (it can be visible in the jail only if it was rescued into GlobalHome,
// because the machine mount hides the wsState one), and a file only GlobalHome has (visible
// only if the machine mount landed at all). Neither is .credentials.json: on the maintainer's
// Mac that is a live login.
//
// ⚠ A PRIVATE STATE DIR, and YOLO_NO_AUTO_IMAGE_REAP. The rescue writes into GlobalHome, which
// the harness otherwise links to the machine's real store — so this test takes
// macArchivePrivateState's private ~/.local/share/yolo-jail, and the reap hatch for the reason
// macArchiveEnv gives: a private state dir knows only this test's workspace, so an image reap
// would take every other jail image on the self-hosted Mac.
func TestAppleContainerRescuesAStrandedSharedDir(t *testing.T) {
	const fix = "#2"
	requireAppleContainer(t)
	requireJail(t)
	packHome(t, `{"packs": ["claude"]}`)
	macArchivePrivateState(t)
	dir := writeProject(t, `{"network": {"mode": "bridge"}}`)

	const shared = ".claude-shared-credentials"
	nonce := acParityNonce()
	rescueName, machineName := "yolo-it-rescue-"+nonce, "yolo-it-machine-"+nonce
	globalShared := filepath.Join(paths.GlobalStorageUnder(os.Getenv("HOME")), "home", shared)
	for path, body := range map[string]string{
		filepath.Join(paths.WorkspaceHomeState(dir), shared, rescueName): "STRANDED-" + nonce,
		filepath.Join(globalShared, machineName):                         "MACHINE-" + nonce,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res := acParityRun(t, fix, dir, strings.Join([]string{
		`echo "=== RESCUED ==="; cat ~/` + shared + `/` + rescueName + ` 2>&1`,
		`echo "=== MACHINE ==="; cat ~/` + shared + `/` + machineName + ` 2>&1`,
		`echo "=== END ==="`,
	}, "\n"), "YOLO_NO_AUTO_IMAGE_REAP=1")

	rescued := strings.Contains(section(res.stdout, "=== RESCUED ===", "=== MACHINE ==="), "STRANDED-"+nonce)
	machine := strings.Contains(section(res.stdout, "=== MACHINE ===", "=== END ==="), "MACHINE-"+nonce)
	_, err := os.Stat(filepath.Join(globalShared, rescueName))
	onHost := err == nil
	evidence := fmt.Sprintf("jail read the rescued file: %v; jail read the GlobalHome-only file: %v; "+
		"rescue copy on the host in GlobalHome: %v\n%s", rescued, machine, onHost,
		section(res.stdout, "=== RESCUED ===", "=== END ==="))

	switch {
	case rescued && machine:
		acParityRecord(t, fix, true, "the stranded copy was rescued into GlobalHome and the jail "+
			"reads it through the machine-wide mount, which shadows the wsState copy beneath it",
			evidence)
	case machine:
		acParityRecord(t, fix, false, "the machine-wide mount landed and shadows the stranded "+
			"copy, and the rescued file is NOT visible through it — an Apple Container user "+
			"upgrading past #1 would find their shared credential gone. The host-side line above "+
			"says whether the copy (prepareWsState's migrateOldOverlay loop) or the mount failed",
			evidence)
	case rescued:
		acParityRecord(t, fix, false, "the jail read the stranded copy but NOT the GlobalHome-only "+
			"file, so it is reading the wsState bind: fix #1's machine-wide mount did not land "+
			"here at all, and the rescue is moot until it does (TestAppleContainerMachineWideTierArrives "+
			"is the direct check)", evidence)
	default:
		acParityRecord(t, fix, false, "neither probe file is readable in the jail: the "+
			"machine-wide dir is mounted from somewhere this test did not seed", evidence)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// backend-parity.md §5 #3: a pack reads-host grant crosses.
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestAppleContainerReadsHostGrantArrives asks fix #3 (`e3c995b6`) on the hardware.
//
// The claude pack's `~/.claude/settings.json` surface declares `readsHost`: the user's own copy
// is a layer of the composed file. On this backend the host file is COPIED into the home
// (acMaterialize into /home/agent/.yolo-ctx) and YOLO_CTX_ROOT tells the entrypoint where to
// read it — packhostgrants.go's hostFileArgs. Linux pins that argv; the hardware question is
// whether the copy arrives and is COMPOSED, which only a probe of the rendered file answers.
//
// The probe is a key no pack layer sets, in the ISOLATED home's settings.json (the harness's
// temp home, never the machine's). Two negative shapes are distinguished: the key missing from
// a rendered file (the surface composed from its defaults, the original defect), and the
// launch refusing because the jail could not read a layer the launcher delivered — the fail-
// closed read (OQ-CO10) doing its job on a copy that did not arrive.
func TestAppleContainerReadsHostGrantArrives(t *testing.T) {
	const fix = "#3"
	dir := appleContainerWorkspace(t)
	// The machine's host-render mark would otherwise label this home's hand-written
	// settings.json "yolo's own render", and the surface would compose without it for a
	// reason that is not #3 (hostprovenanceisolation_test.go). MEASURED 2026-09-25: run
	// 36170072271 on the maintainer's Mac reported DOES NOT HOLD with the copy present
	// under YOLO_CTX_ROOT, the one shape that label produces.
	privateHostProvenance(t, os.Getenv("HOME"), hostHome)
	if entrypoint.HostSurfaceRendered(os.Getenv("HOME"), claudeSettingsSurface) {
		t.Fatalf("%s %s: this home still carries a host-render mark for claude/settings, so "+
			"the launcher would drop its settings.json as yolo's own render — NOTHING WOULD BE "+
			"MEASURED about #3 (privateHostProvenance did not take)", acParityTag, fix)
	}
	nonce := acParityNonce()
	settings := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"yoloItReadsHostProbe": "`+nonce+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := strings.Join([]string{
		`echo "=== CTX ==="; echo "YOLO_CTX_ROOT=${YOLO_CTX_ROOT-UNSET}"`,
		`[ -n "${YOLO_CTX_ROOT-}" ] && find "$YOLO_CTX_ROOT" -type f 2>&1 | head -20`,
		`echo "=== SETTINGS ==="; cat ~/.claude/settings.json 2>&1`,
		`echo "=== END ==="`,
	}, "\n")
	res := runYolo(t, dir, script, appleContainerEnv())
	if res.rc != 0 && strings.Contains(res.combined(), "cannot be read there") {
		acParityRecord(t, fix, false, "the launcher delivered the host layer and the jail could "+
			"not read it, so the fail-closed host-layer read refused the launch: the "+
			"materialized copy under YOLO_CTX_ROOT did not arrive", lastLines(res.combined(), 40))
		return
	}
	if res.rc != 0 || !strings.Contains(res.stdout, "=== END ===") {
		t.Fatalf("%s %s: the launch did not run its probe (rc=%d), so NOTHING WAS MEASURED.\n"+
			"stdout:\n%s\nstderr:\n%s", acParityTag, fix, res.rc,
			lastLines(res.stdout, 60), lastLines(res.stderr, 60))
	}
	rendered := section(res.stdout, "=== SETTINGS ===", "=== END ===")
	if !strings.Contains(rendered, "{") {
		t.Fatalf("%s %s: ~/.claude/settings.json did not render at all, so there is no surface "+
			"to ask about its host layer — nothing was measured:\n%s", acParityTag, fix, rendered)
	}
	evidence := section(res.stdout, "=== CTX ===", "=== END ===")
	if strings.Contains(rendered, nonce) {
		acParityRecord(t, fix, true, "the user's own settings.json crossed as a copy under "+
			"YOLO_CTX_ROOT and was composed into the surface", evidence)
		return
	}
	acParityRecord(t, fix, false, "~/.claude/settings.json rendered WITHOUT the user's own "+
		"layer — the surface composed from its defaults, which is the defect #3 fixed", evidence)
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// backend-parity.md §5 #4: a host_files FILE source arrives, unmasked.
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestAppleContainerHostFilesSourceArrivesUnmasked asks fix #4 (`c22e25b5`) on the hardware.
//
// The defect was worse than an absence: the entrypoint swallowed the missing read and wrote the
// destination anyway at the `readonly` default mode, so the user got an EMPTY 0o444 file where
// theirs should be. The fix copies a FILE source into /home/agent/.yolo-ctx/host-user/<slug>
// (hostfiles.go's hostUserFileArgs). A source-bearing entry is user scope only, so it goes in
// the isolated user config, with its source in the isolated home.
func TestAppleContainerHostFilesSourceArrivesUnmasked(t *testing.T) {
	const fix = "#4"
	requireAppleContainer(t)
	requireJail(t)
	nonce := acParityNonce()
	packHome(t, `{"host_files": [{"path": "~/.config/yolo-it-ac/probe.txt", "source": "~/yolo-it-ac-source.txt"}]}`)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "yolo-it-ac-source.txt"),
		[]byte("HOSTFILE-"+nonce+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, `{"network": {"mode": "bridge"}}`)

	res := acParityRun(t, fix, dir, strings.Join([]string{
		`f=~/.config/yolo-it-ac/probe.txt`,
		`echo "=== FILE ==="; if [ -e "$f" ]; then echo "SIZE=$(wc -c < "$f" | tr -d ' ')"; ls -l "$f"; cat "$f"; else echo ABSENT; fi`,
		`echo "=== END ==="`,
	}, "\n"))
	got := section(res.stdout, "=== FILE ===", "=== END ===")
	switch {
	case strings.Contains(got, "HOSTFILE-"+nonce):
		acParityRecord(t, fix, true, "the host_files FILE source arrived with its bytes", got)
	case strings.Contains(got, "SIZE=0"):
		acParityRecord(t, fix, false, "the destination exists and is EMPTY — the masking defect "+
			"#4 fixed: the copy under .yolo-ctx/host-user did not arrive and the entrypoint "+
			"wrote the destination anyway", got)
	default:
		acParityRecord(t, fix, false, "the destination did not receive the source's bytes", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// backend-parity.md §5 #8: the briefing advertises no loophole this backend never started.
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestAppleContainerBriefingAdvertisesNoLoopholes asks fix #8 (`a639394d`) on the hardware.
//
// On this backend the claude pack's `claude-oauth-broker` is reported inert (backendInertReason:
// nothing crosses container→host on `container` 1.1.0), so the briefing must not carry the
// section headed "Loopholes — host capabilities wired into this jail" — an agent that reads a
// capability list plans around it. Linux pins the composed text (briefingbackend_test.go); what
// it cannot show is the briefing that ARRIVES in the agent's home on this backend's single
// whole-home bind. A launch with no briefing at all is not an answer and fails.
func TestAppleContainerBriefingAdvertisesNoLoopholes(t *testing.T) {
	const fix = "#8"
	dir := appleContainerWorkspace(t)
	res := acParityRun(t, fix, dir, strings.Join([]string{
		`echo "=== BRIEFING ==="; cat ~/.claude/CLAUDE.md 2>&1`,
		`echo "=== END ==="`,
	}, "\n"))
	briefing := section(res.stdout, "=== BRIEFING ===", "=== END ===")
	if !strings.Contains(briefing, "# YOLO Jail Environment") {
		t.Fatalf("%s %s: no yolo briefing arrived at ~/.claude/CLAUDE.md, so there is nothing to "+
			"ask about its loophole section — nothing was measured (and a missing briefing is a "+
			"defect of its own):\n%s", acParityTag, fix, lastLines(briefing, 30))
	}
	inertLine := strings.Contains(res.combined(), "loophole claude-oauth-broker is inert on this backend")
	const heading = "## Loopholes — host capabilities wired into this jail"
	if i := strings.Index(briefing, heading); i >= 0 {
		acParityRecord(t, fix, false, "the delivered briefing advertises loopholes on a backend "+
			"that starts none the jail can reach — the defect #8 fixed",
			fmt.Sprintf("launch reported the broker inert: %v\n%s", inertLine, lastLines(briefing[i:], 12)))
		return
	}
	acParityRecord(t, fix, true, "the delivered briefing carries no loophole section",
		fmt.Sprintf("launch reported the broker inert: %v", inertLine))
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// backend-parity.md §5 #10: an explicit `network.mode: host` still publishes ports.
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestAppleContainerExplicitHostModeKeepsPublishedPorts asks fix #10 (`8ab03d2e`) on the
// hardware, as it stands today.
//
// Apple Container takes no `--net` selector, so an explicit `network.mode: "host"` does nothing
// there. It used to make things WORSE than the default — both port keys were gated on bridge
// mode, so asking for host mode silently dropped every published port. Today the launch warns,
// and the port gate reads the APPLIED mode (assemble.go), which is bridged on this backend
// either way. The hardware question is the traffic, not the argv: does a server in the jail,
// published with `network.ports`, answer the Mac under an explicit host mode just as it does
// under the default?
//
// So it is asked twice, default first as the CONTROL. If the default cannot publish either, the
// record says `-p` does not work on this backend at all, which is a different and larger finding
// than #10.
func TestAppleContainerExplicitHostModeKeepsPublishedPorts(t *testing.T) {
	const fix = "#10"
	requireAppleContainer(t)
	requireJail(t)
	bridge := acPublishedPortProbe(t, fix, "bridge")
	host := acPublishedPortProbe(t, fix, "host")
	evidence := fmt.Sprintf("default (bridge): %s\nexplicit host:    %s\nhost-mode warning printed: %v\n"+
		"address family: %s\n\nin-jail evidence, default (bridge):\n%s\n\nin-jail evidence, explicit host:\n%s",
		bridge.describe(), host.describe(), host.warned, acPortFamilyFinding(bridge, host),
		bridge.jailDiag, host.jailDiag)
	switch {
	case bridge.reached() && host.reached():
		acParityRecord(t, fix, true, "a published port answers the Mac under an explicit "+
			"`network.mode: host` exactly as under the default", evidence)
	case bridge.reached():
		acParityRecord(t, fix, false, "the default publishes the port and an explicit host mode "+
			"does NOT — the defect #10 fixed is back: asking for host mode drops `network.ports`", evidence)
	case host.reached():
		acParityRecord(t, fix, false, "an explicit host mode published the port and the default "+
			"did NOT — the reverse of the defect, and a different bug", evidence)
	default:
		acParityRecord(t, fix, false, "NEITHER mode published the port: `network.ports` does not "+
			"reach the Mac on this backend at all, so #10's claim is vacuous here and the larger "+
			"finding is `-p` itself", evidence)
	}
}

// acPortResult is what one published-port launch showed.
//
// Each launch publishes TWO in-jail servers, because the first Mac run (2026-09-25, container
// CLI 1.1.0) found every dial to a single IPv4-only socat answered by "connected, then EOF" in
// BOTH modes: something on the Mac accepted the host port, and nothing answered behind it. That
// is what a forwarder does when its connection into the container is refused. socat 1.8's
// TCP-LISTEN binds IPv4 only (an IPv6 connect to it is refused), while Apple's own --publish
// examples bind `::`. So one listener is IPv4-only and one is dual-stack, and which of them the
// Mac reaches is the answer, not a guess. jailDiag is the jail's own view of the same ports, so
// "the server was not running" can never pass for "the forwarder cannot reach it".
type acPortResult struct {
	mode     string
	rc       int
	warned   bool
	v4       acListenerResult // socat TCP-LISTEN: IPv4 only
	dual     acListenerResult // socat TCP6-LISTEN,ipv6only=0: IPv4 and IPv6
	jailDiag string
}

// acListenerResult is the Mac's side of one published listener.
type acListenerResult struct {
	reached bool
	dials   int
	lastErr string
}

// reached reports whether EITHER listener answered: #10 asks whether a published port reaches
// the Mac at all, and which family it needed is acPortFamilyFinding's question.
func (r acPortResult) reached() bool { return r.v4.reached || r.dual.reached }

func (r acPortResult) describe() string {
	return fmt.Sprintf("IPv4-only listener: reached=%v after %d dial(s), last error: %s; "+
		"dual-stack listener: reached=%v after %d dial(s), last error: %s; launch rc=%d",
		r.v4.reached, r.v4.dials, r.v4.lastErr, r.dual.reached, r.dual.dials, r.dual.lastErr, r.rc)
}

// acPortFamilyFinding states what the two listeners, across both launches, say about the
// family Apple Container's forwarder connects with.
func acPortFamilyFinding(runs ...acPortResult) string {
	var v4, dual bool
	for _, r := range runs {
		v4 = v4 || r.v4.reached
		dual = dual || r.dual.reached
	}
	switch {
	case v4:
		return "an IPv4-only in-jail listener IS reached, so the family is not what blocks a published port"
	case dual:
		return "ONLY the dual-stack listener is reached: the forwarder connects over IPv6, so an " +
			"in-jail server published on this backend must bind `::`, not 0.0.0.0"
	default:
		return "neither listener is reached, so the family is not the cause; read the in-jail evidence"
	}
}

// The jail side of the two published ports. Fixed, because the jail's network namespace is its
// own; the host sides are ephemeral ports picked per launch.
const (
	acPublishedPortJailPort     = 18765 // the IPv4-only listener
	acPublishedPortDualJailPort = 18766 // the dual-stack listener
)

// acPublishedPortProbe launches one jail publishing two socat token servers, and dials each
// from this process while the jail waits.
//
// THE JAIL WAITS FOR THE HOST, not the reverse: the dialers write a marker into the workspace
// (bind-mounted at /workspace) once both have an answer, and the jail's script exits on it, or
// on its own bound. So the launch is never cut short while a dial is in flight, and a jail that
// publishes nothing costs the bound and not the per-command timeout.
func acPublishedPortProbe(t *testing.T, fix, mode string) acPortResult {
	t.Helper()
	v4Host, dualHost := acFreeLoopbackPort(t), acFreeLoopbackPort(t)
	nonce := "YOLO-AC-PORT-" + acParityNonce()
	v4Token, dualToken := nonce+"-V4", nonce+"-DUAL"
	dir := writeProject(t, fmt.Sprintf(`{"network": {"mode": %q, "ports": ["%d:%d", "%d:%d"]}}`,
		mode, v4Host, acPublishedPortJailPort, dualHost, acPublishedPortDualJailPort))
	const marker = ".yolo-it-dialed"

	stop := make(chan struct{})
	r := acPortResult{mode: mode, v4: acListenerResult{lastErr: "none"}, dual: acListenerResult{lastErr: "none"}}
	var dialers sync.WaitGroup
	dial := func(port int, token string, into *acListenerResult) {
		defer dialers.Done()
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		for {
			select {
			case <-stop:
				return
			default:
			}
			into.dials++
			if line, err := acDialLine(addr); err != nil {
				into.lastErr = err.Error()
			} else if strings.Contains(line, token) {
				into.reached = true
				return
			} else {
				into.lastErr = fmt.Sprintf("connected, read %q instead of the token", line)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	dialers.Add(2)
	go dial(v4Host, v4Token, &r.v4)
	go dial(dualHost, dualToken, &r.dual)
	var marked sync.WaitGroup
	marked.Add(1)
	go func() {
		defer marked.Done()
		dialers.Wait()
		_ = os.WriteFile(filepath.Join(dir, marker), []byte("done\n"), 0o644)
	}()

	res := runYolo(t, dir, acPublishedPortScript(v4Token, dualToken, marker), appleContainerEnv())
	close(stop)
	marked.Wait()
	r.rc = res.rc
	r.warned = strings.Contains(res.combined(), `network.mode "host" is NOT honored on Apple Container`)
	r.jailDiag = strings.TrimSpace(section(res.stdout, "=== DIAG ===", "=== END DIAG ==="))
	if res.rc != 0 || !strings.Contains(res.stdout, "LISTENING") {
		t.Fatalf("%s %s: the %s-mode launch did not start its servers (rc=%d), so nothing was "+
			"measured.\nstdout:\n%s\nstderr:\n%s", acParityTag, fix, mode, res.rc,
			lastLines(res.stdout, 40), lastLines(res.stderr, 40))
	}
	return r
}

// acPublishedPortScript is the jail's half of acPublishedPortProbe: start both listeners, print
// the jail's own view of them between the DIAG markers, then wait for the host's marker.
//
// The in-jail evidence is what separates the failure classes a Mac-side EOF cannot: whether
// each socat is alive (its stderr when not), whether the kernel holds a LISTEN socket for each
// port and in which family (/proc/net/tcp and tcp6; 18765 and 18766 are 0x494D and 0x494E), and
// whether each server answers the jail itself on loopback and on every non-loopback address the
// container has. A server that answers its own non-loopback address but not the Mac is reachable
// in the container and lost in the forwarder.
func acPublishedPortScript(v4Token, dualToken, marker string) string {
	v4Port, dualPort := strconv.Itoa(acPublishedPortJailPort), strconv.Itoa(acPublishedPortDualJailPort)
	return strings.Join([]string{
		`socat TCP-LISTEN:` + v4Port + `,fork,reuseaddr SYSTEM:'echo ` + v4Token + `' 2>/tmp/yolo-it-socat-v4.err &`,
		`v4pid=$!`,
		`socat TCP6-LISTEN:` + dualPort + `,ipv6only=0,fork,reuseaddr SYSTEM:'echo ` + dualToken + `' 2>/tmp/yolo-it-socat-dual.err &`,
		`dualpid=$!`,
		`sleep 1`,
		`echo LISTENING`,
		`echo "=== DIAG ==="`,
		`for l in v4:$v4pid dual:$dualpid; do name=${l%%:*}; pid=${l#*:}; ` +
			`if kill -0 "$pid" 2>/dev/null; then echo "$name socat: alive"; ` +
			`else echo "$name socat: DEAD — $(cat /tmp/yolo-it-socat-$name.err 2>&1)"; fi; done`,
		`echo "LISTEN sockets (/proc/net/tcp, then tcp6):"`,
		`awk '$4=="0A" && ($2 ~ /:494D$/ || $2 ~ /:494E$/) {print FILENAME": "$2}' /proc/net/tcp /proc/net/tcp6 2>&1`,
		`addrs4=$(awk '/32 host/ {print f} {f=$2}' /proc/net/fib_trie 2>/dev/null | sort -u | grep -v '^127\.')`,
		`addrs6=$(awk '$4=="00" {print $1}' /proc/net/if_inet6 2>/dev/null | sed 's/\(....\)/\1:/g; s/:$//')`,
		`echo "non-loopback addresses: v4=[$(echo $addrs4)] v6=[$(echo $addrs6)]"`,
		`for port in ` + v4Port + ` ` + dualPort + `; do ` +
			`for a in TCP4:127.0.0.1 'TCP6:[::1]' $(for x in $addrs4; do echo TCP4:$x; done) $(for x in $addrs6; do echo "TCP6:[$x]"; done); do ` +
			`echo "self-dial $a:$port -> $(timeout 4 socat -T2 - "$a:$port" </dev/null 2>&1 | head -1)"; done; done`,
		`echo "=== END DIAG ==="`,
		`for _ in $(seq 1 180); do [ -f /workspace/` + marker + ` ] && break; sleep 0.5; done`,
		`echo "=== END ==="`,
	}, "\n")
}

// acDialLine connects to addr and reads one line, bounded, so a dropped SYN or a silent peer
// cannot hold the poll.
func acDialLine(addr string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("connected, then %w", err)
	}
	return strings.TrimSpace(line), nil
}

// acFreeLoopbackPort asks the kernel for a free port and releases it. A small race, and a
// harmless one: a port taken in between makes the launch fail to publish, which the record
// then shows for both modes alike rather than for one.
func acFreeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("picking a free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

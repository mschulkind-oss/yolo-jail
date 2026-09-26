package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
// than #10. When the Mac-side evidence carries macOS Local Network privacy's signature, the
// evidence says so first, with the fix (acLocalNetworkFinding), and a verdict in which neither
// mode reached the Mac defers to it (acPortVerdict): that answer is about the runner's
// permissions, not about yolo.
func TestAppleContainerExplicitHostModeKeepsPublishedPorts(t *testing.T) {
	const fix = "#10"
	requireAppleContainer(t)
	requireJail(t)
	bridge := acPublishedPortProbe(t, fix, "bridge")
	host := acPublishedPortProbe(t, fix, "host")
	evidence := acPortEvidence(bridge, host)
	holds, finding := acPortVerdict(bridge, host)
	acParityRecord(t, fix, holds, finding, evidence)
}

// acPortVerdict is the #10 verdict: whether the fix holds, and the finding line printed with it.
//
// When neither mode reached the Mac and the Mac-side evidence carries Local Network privacy's
// signature, the finding defers to the DIAGNOSIS the evidence opens with (acLocalNetworkFinding)
// instead of calling `-p` itself the finding: that run measured the runner's permissions, and a
// verdict line blaming `-p` above a diagnosis blaming the runner would contradict it. The verdict
// is DOES NOT HOLD either way, since no port was reached.
func acPortVerdict(bridge, host acPortResult) (bool, string) {
	switch {
	case bridge.reached() && host.reached():
		return true, "a published port answers the Mac under an explicit " +
			"`network.mode: host` exactly as under the default"
	case bridge.reached():
		return false, "the default publishes the port and an explicit host mode " +
			"does NOT — the defect #10 fixed is back: asking for host mode drops `network.ports`"
	case host.reached():
		return false, "an explicit host mode published the port and the default " +
			"did NOT — the reverse of the defect, and a different bug"
	case acLocalNetworkFinding(bridge, host) != "":
		return false, "NEITHER mode published the port, and the Mac-side evidence carries macOS " +
			"Local Network privacy's signature, so this run says nothing yet about #10 or about " +
			"`-p` on this backend — see the DIAGNOSIS the evidence opens with"
	default:
		return false, "NEITHER mode published the port: `network.ports` does not " +
			"reach the Mac on this backend at all, so #10's claim is vacuous here and the larger " +
			"finding is `-p` itself"
	}
}

// acPortEvidence is the #10 verdict's evidence: both launches' dial timelines, the family
// finding, and each side's diagnostics — headed by the Local Network privacy diagnosis when the
// Mac-side evidence carries its signature.
func acPortEvidence(bridge, host acPortResult) string {
	evidence := fmt.Sprintf("default (bridge): %s\nexplicit host:    %s\nhost-mode warning printed: %v\n"+
		"address family: %s\n\nin-jail evidence, default (bridge):\n%s\n\nin-jail evidence, explicit host:\n%s"+
		"\n\nMac-side evidence, default (bridge):\n%s\n\nMac-side evidence, explicit host:\n%s",
		bridge.describe(), host.describe(), host.warned, acPortFamilyFinding(bridge, host),
		bridge.jailDiag, host.jailDiag, bridge.mac, host.mac)
	if lnp := acLocalNetworkFinding(bridge, host); lnp != "" {
		evidence = lnp + "\n\n" + evidence
	}
	return evidence
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
	// mac is the Mac's view beyond the published port, gathered once the jail has written its
	// address (acPortMacDiag): the container's own vmnet address dialed directly, the published
	// host port dialed on the vmnet gateway and on [::1], the route to the container, and what
	// `container inspect` and `lsof` say was published. It separates "Apple never bound the host
	// port" from "Apple bound it somewhere other than 127.0.0.1" from "the vmnet path itself is
	// down", which the second Mac run (2026-09-25: both listeners alive and self-reachable, every
	// Mac dial refused) could not.
	mac acMacEvidence
}

// acListenerResult is the Mac's side of one dialed target: EVERY dial it made, in order.
//
// Every dial, not the last one. The fourth Mac run's record (run 36193230191) kept only each
// listener's last error, and the last loopback dials land after the jail has exited, so its
// "refused" read as "nothing listens on the Mac's loopback" while `lsof` showed the `container`
// process listening there (backend-parity.md §5.4). So each dial keeps its time, the jail's phase
// when it started, and the KIND of answer, and describe prints them run-length encoded:
// "while the jail ran: 12× accepted, then reset", then "after the jail's script ended: 3×
// refused".
type acListenerResult struct {
	addr    string // host:port dialed; empty when there was no address to dial
	reached bool
	dials   []acDial
}

// acDial is one dial's outcome.
type acDial struct {
	at    time.Duration // since the probe started (acProbeClock.start)
	phase acPhase       // the jail's phase when the dial started
	kind  string        // an acKind* label, or a verbatim one for an answer none of them names
}

// acPhase is where the jail was when a dial started, read from the marker files it writes into
// the workspace the Mac shares with it (acJailPhase).
type acPhase string

const (
	acPhaseStarting acPhase = "before the jail reported listening" // no acPortListenFile yet
	acPhaseRunning  acPhase = "while the jail ran"                 // acPortListenFile written: both servers started and given a second to bind
	acPhaseEnded    acPhase = "after the jail's script ended"      // acPortExitFile written: the container is going away
)

// The kinds of answer a dial can get. The connect-time kinds are what the Mac's kernel or a
// policy said before any byte was exchanged; the "accepted" kinds mean something on the far
// side took the connection and then did not answer with the token. EHOSTUNREACH with a live
// route is the Local Network privacy signature (acMacEvidence.localNetworkDenied).
const (
	acKindReached     = "REACHED"
	acKindRefused     = "refused (ECONNREFUSED)"
	acKindNoRoute     = "no route to host (EHOSTUNREACH)"
	acKindNetUnreach  = "network unreachable (ENETUNREACH)"
	acKindConnReset   = "reset during connect (ECONNRESET)"
	acKindConnTimeout = "connect timed out"
	acKindEOF         = "accepted, then EOF"
	acKindReset       = "accepted, then reset (ECONNRESET)"
	acKindSilent      = "accepted, then no reply before the read deadline"
)

// reached reports whether EITHER listener answered: #10 asks whether a published port reaches
// the Mac at all, and which family it needed is acPortFamilyFinding's question.
func (r acPortResult) reached() bool { return r.v4.reached || r.dual.reached }

func (r acPortResult) describe() string {
	return fmt.Sprintf("launch rc=%d\n%s\n%s", r.rc,
		acIndent(r.v4.describe("IPv4-only listener"), "  "),
		acIndent(r.dual.describe("dual-stack listener"), "  "))
}

// describe is one target's record: a headline, then its timeline, one line per run of dials
// that shared a phase and a kind.
func (r acListenerResult) describe(label string) string {
	head := label + " " + r.addr + ": "
	switch {
	case r.addr == "":
		return label + ": no address to dial"
	case r.reached:
		head += fmt.Sprintf("REACHED on dial %d", len(r.dials))
	case len(r.dials) == 0:
		head += "never dialed"
	default:
		head += fmt.Sprintf("not reached in %d dial(s)", len(r.dials))
	}
	lines := []string{head}
	for _, l := range r.timeline() {
		lines = append(lines, "    "+l)
	}
	return strings.Join(lines, "\n")
}

// timeline run-length encodes the dials by phase and kind, in order, each run with the time
// span it covered.
func (r acListenerResult) timeline() []string {
	var out []string
	for i := 0; i < len(r.dials); {
		first := r.dials[i]
		j := i
		for j+1 < len(r.dials) && r.dials[j+1].phase == first.phase && r.dials[j+1].kind == first.kind {
			j++
		}
		span := fmt.Sprintf("t=%.1fs", first.at.Seconds())
		if j > i {
			span = fmt.Sprintf("t=%.1fs–%.1fs", first.at.Seconds(), r.dials[j].at.Seconds())
		}
		out = append(out, fmt.Sprintf("%s: %d× %s (%s)", first.phase, j-i+1, first.kind, span))
		i = j + 1
	}
	return out
}

// acIndent prefixes every line of s.
func acIndent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
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

// acLocalNetworkFinding names macOS Local Network privacy, with the fix, when any launch's
// Mac-side evidence carries its signature (acMacEvidence.localNetworkDenied), and is empty
// otherwise.
//
// The diagnosis is about THIS test process, which is the one binary whose dial is recorded; the
// step to the container helpers behind the published port is inferred, and the text says so.
// Measured on the runner's Mac on 2026-09-25 (backend-parity.md §5.4, `b4a48e99`): every
// non-Apple binary tried got EHOSTUNREACH to the container and to the LAN hosts dialed while a
// route existed, and still reached the Mac's own vmnet address and the LAN gateway; Apple's own
// binaries reached all of them. Both verdicts still pass: this is still an experiment.
//
// When a published port DID answer in either launch, whatever forwards it reaches the container,
// so the inference about the helpers is refuted and the text says instead that the denial is this
// process's own and does not decide #10. That is the likely shape of the run after the helpers are
// granted Local Network access and the `go test` binary is not.
func acLocalNetworkFinding(runs ...acPortResult) string {
	var where []string
	published := false
	for _, r := range runs {
		published = published || r.reached()
		if r.mac.localNetworkDenied() {
			where = append(where, fmt.Sprintf("%s launch: the container at %s, route on %s",
				r.mode, r.mac.route.dest, r.mac.route.iface))
		}
	}
	if len(where) == 0 {
		return ""
	}
	head := "DIAGNOSIS — macOS LOCAL NETWORK PRIVACY. This test process got EHOSTUNREACH dialing " +
		"the container's own address while the Mac holds a live, directly attached route to it (" +
		strings.Join(where, "; ") + "). That is Local Network privacy's signature: macOS refuses a " +
		"binary it has not granted Local Network access before any packet leaves the Mac " +
		"(Apple TN3179), and the self-hosted runner's Mac measured exactly that for every " +
		"non-Apple binary tried on 2026-09-25 (docs/design/backend-parity.md §5.4)."
	if published {
		return head + " A published port DID answer, so whatever forwards it reaches the " +
			"container: the denial is this test process's own and does not decide #10. For the " +
			"direct dials to measure the vmnet path as well, grant Local Network access to the " +
			"process running `go test`; whether a grant to the runner covers the `go test` binary " +
			"it spawns, relinked every run, is unmeasured."
	}
	return head + " The Homebrew " +
		"`container` helpers (container-apiserver, container-runtime-linux, " +
		"container-network-vmnet) are ad-hoc signed, non-Apple binaries in the same class, so the " +
		"forwarder behind a published port is expected to be refused the same dial, which would " +
		"show on the published port as accepted, then reset; that step is inferred, not measured.\n" +
		"FIX, ON THE MAC AND NOT IN YOLO: grant Local Network access (System Settings → Privacy & " +
		"Security → Local Network) to the runner and to the container helpers, or install Apple's " +
		"Developer-ID-signed `container` package in place of Homebrew's build, then rerun. A grant " +
		"is keyed to a code signature, so an ad-hoc-signed binary's grant may not survive a " +
		"rebuild or a `brew upgrade`; whether a grant to the runner covers the `go test` binary it " +
		"spawns, relinked every run, is unmeasured. Until then this experiment measures the " +
		"runner's Local Network permissions, not #10."
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
	clock := acProbeClock{start: time.Now(), dir: dir}

	stop := make(chan struct{})
	r := acPortResult{mode: mode}
	var dialers sync.WaitGroup
	dialers.Add(3)
	go func() {
		defer dialers.Done()
		r.v4 = acPollListener(stop, acLoopbackAddr(v4Host), v4Token, clock, 500*time.Millisecond, 0)
	}()
	go func() {
		defer dialers.Done()
		r.dual = acPollListener(stop, acLoopbackAddr(dualHost), dualToken, clock, 500*time.Millisecond, 0)
	}()
	go func() {
		defer dialers.Done()
		r.mac = acPortMacDiag(stop, clock, v4Host, dualHost, v4Token, dualToken)
	}()
	var marked sync.WaitGroup
	marked.Add(1)
	go func() {
		defer marked.Done()
		dialers.Wait()
		_ = os.WriteFile(filepath.Join(dir, marker), []byte("done\n"), 0o644)
	}()
	halt := acHaltOnCleanup(t, stop, marked.Wait)

	res := runYolo(t, dir, acPublishedPortScript(v4Token, dualToken, marker), appleContainerEnv())
	halt()
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

// acHaltOnCleanup returns halt, which closes stop once and then waits for the dialers (wait).
// acPublishedPortProbe calls it right after the launch, and t.Cleanup calls it again: a launch
// that dies in runCommand's t.Fatalf (a timeout, a yolo that failed to start, a failed image
// build) skips the explicit call, because Fatalf is runtime.Goexit. Without the cleanup, the two
// loopback polls, which have no dial cap, would dial at 2 Hz and grow their records until the
// test binary exits. The cleanup is registered after writeProject's, so it runs first (cleanups
// are LIFO) and the dialers' marker is written before the workspace is removed.
func acHaltOnCleanup(t *testing.T, stop chan struct{}, wait func()) func() {
	t.Helper()
	var once sync.Once
	halt := func() {
		once.Do(func() { close(stop) })
		wait()
	}
	t.Cleanup(halt)
	return halt
}

func acLoopbackAddr(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

// acProbeClock stamps each dial with its time since the probe started and the jail's phase.
type acProbeClock struct {
	start time.Time
	dir   string // the workspace the jail writes its marker files into
}

func (c acProbeClock) stamp() acDial {
	return acDial{at: time.Since(c.start), phase: acJailPhase(c.dir)}
}

// acJailPhase reads the jail's phase from the marker files acPublishedPortScript writes into
// the shared workspace: its listen file once both servers are started and given a second to
// bind, its exit file as the script's last act.
//
// Not the address file, which the jail writes only after its whole in-jail diagnostic, more than
// a second after the servers came up: a dial in that window would have been stamped as made
// before anything listened, which is the misreading the phases exist to prevent.
func acJailPhase(dir string) acPhase {
	if _, err := os.Stat(filepath.Join(dir, acPortExitFile)); err == nil {
		return acPhaseEnded
	}
	if _, err := os.Stat(filepath.Join(dir, acPortListenFile)); err == nil {
		return acPhaseRunning
	}
	return acPhaseStarting
}

// acPollListener dials addr every `every` until the token answers, stop closes, or maxDials dials
// have been made (maxDials 0 is no bound), and returns every dial it made.
func acPollListener(stop <-chan struct{}, addr, token string, clock acProbeClock, every time.Duration, maxDials int) acListenerResult {
	r := acListenerResult{addr: addr}
	for n := 1; ; n++ {
		select {
		case <-stop:
			return r
		default:
		}
		d := clock.stamp()
		line, err := acDialLine(addr)
		d.kind = acDialKind(line, err, token)
		r.dials = append(r.dials, d)
		if d.kind == acKindReached {
			r.reached = true
			return r
		}
		if maxDials > 0 && n >= maxDials {
			return r
		}
		select {
		case <-stop:
			return r
		case <-time.After(every):
		}
	}
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
//
// Its two phase markers are how the Mac tells the phases apart (acJailPhase): acPortListenFile
// as it prints LISTENING, once both servers are started and given a second to bind, and
// acPortExitFile as the script's last act, so a dial made while the container is going away is
// never read as one made while it served.
func acPublishedPortScript(v4Token, dualToken, marker string) string {
	v4Port, dualPort := strconv.Itoa(acPublishedPortJailPort), strconv.Itoa(acPublishedPortDualJailPort)
	return strings.Join([]string{
		`socat TCP-LISTEN:` + v4Port + `,fork,reuseaddr SYSTEM:'echo ` + v4Token + `' 2>/tmp/yolo-it-socat-v4.err &`,
		`v4pid=$!`,
		`socat TCP6-LISTEN:` + dualPort + `,ipv6only=0,fork,reuseaddr SYSTEM:'echo ` + dualToken + `' 2>/tmp/yolo-it-socat-dual.err &`,
		`dualpid=$!`,
		`sleep 1`,
		`echo listening > /workspace/` + acPortListenFile,
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
		`g=$(awk '$2=="00000000" {print $3; exit}' /proc/net/route 2>/dev/null)`,
		`gw=; [ ${#g} -eq 8 ] && gw=$(printf '%d.%d.%d.%d' 0x${g:6:2} 0x${g:4:2} 0x${g:2:2} 0x${g:0:2})`,
		`echo "default gateway: ${gw:-none}; hostname: $(hostname)"`,
		`echo "$(echo $addrs4 | awk '{print $1}') ${gw:--} $(hostname)" > /workspace/` + acPortAddrFile,
		`for port in ` + v4Port + ` ` + dualPort + `; do ` +
			`for a in TCP4:127.0.0.1 'TCP6:[::1]' $(for x in $addrs4; do echo TCP4:$x; done) $(for x in $addrs6; do echo "TCP6:[$x]"; done); do ` +
			`echo "self-dial $a:$port -> $(timeout 4 socat -T2 - "$a:$port" </dev/null 2>&1 | head -1)"; done; done`,
		`echo "=== END DIAG ==="`,
		`for _ in $(seq 1 180); do [ -f /workspace/` + marker + ` ] && break; sleep 0.5; done`,
		`echo ended > /workspace/` + acPortExitFile,
		`echo "=== END ==="`,
	}, "\n")
}

// acReadErr marks an acDialLine error that came AFTER the connection was accepted, so a dial's
// kind can tell "something accepted, then reset" from "the connect itself was reset".
type acReadErr struct{ err error }

func (e acReadErr) Error() string { return "connected, then " + e.err.Error() }
func (e acReadErr) Unwrap() error { return e.err }

// acDialLine connects to addr and reads one line, bounded, so a dropped SYN or a silent peer
// cannot hold the poll. A failure after the connect is an acReadErr.
func acDialLine(addr string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil && line == "" {
		return "", acReadErr{err}
	}
	return strings.TrimSpace(line), nil
}

// acDialKind names what one acDialLine call got: an acKind* label, a wrong reply quoted, or,
// for an error none of the labels names, the error itself.
func acDialKind(line string, err error, token string) string {
	var accepted acReadErr
	afterAccept := errors.As(err, &accepted)
	switch {
	case err == nil && strings.Contains(line, token):
		return acKindReached
	case err == nil:
		return fmt.Sprintf("accepted, read %q instead of the token", line)
	case afterAccept && errors.Is(err, io.EOF):
		return acKindEOF
	case afterAccept && errors.Is(err, syscall.ECONNRESET):
		return acKindReset
	case afterAccept && acIsTimeout(err):
		return acKindSilent
	case afterAccept:
		return "accepted, then " + acInnermost(accepted.err).Error()
	case errors.Is(err, syscall.ECONNREFUSED):
		return acKindRefused
	case errors.Is(err, syscall.EHOSTUNREACH):
		return acKindNoRoute
	case errors.Is(err, syscall.ENETUNREACH):
		return acKindNetUnreach
	case errors.Is(err, syscall.ECONNRESET):
		return acKindConnReset
	case acIsTimeout(err):
		return acKindConnTimeout
	default:
		return "other: " + err.Error()
	}
}

// acInnermost unwraps err to the bottom of its chain: for a read error, the errno, without the
// *net.OpError around it. That wrapper prints the connection's local address, whose ephemeral
// port changes on every dial, so a kind built from it would never repeat and timeline could merge
// no run of such dials.
func acInnermost(err error) error {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
}

func acIsTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

// acPortAddrFile is where the jail writes "<its first IPv4> <default gateway> <hostname>" for
// acPortMacDiag, in the workspace the Mac shares with it.
const acPortAddrFile = ".yolo-it-addr"

// acPortListenFile is written as the jail prints LISTENING, once both servers are started and
// given a second to bind, marking acPhaseRunning.
const acPortListenFile = ".yolo-it-listening"

// acPortExitFile is the jail script's last write, marking acPhaseEnded.
const acPortExitFile = ".yolo-it-ended"

// acMacEvidence is the Mac-side diagnostic acPortResult.mac describes.
type acMacEvidence struct {
	missing   string // why there is no Mac-side evidence, when there is none
	probes    []acMacProbe
	route     acRoute
	inspect   string
	listeners string
}

// acMacProbe is one Mac-side diagnostic target and what its dials got.
type acMacProbe struct {
	label     string
	container bool // dials the container's own address rather than a published host port
	result    acListenerResult
}

func (e acMacEvidence) String() string {
	if e.missing != "" {
		return e.missing
	}
	lines := make([]string, 0, len(e.probes)+3)
	for _, p := range e.probes {
		lines = append(lines, p.result.describe(p.label))
	}
	lines = append(lines, e.route.text, e.inspect, e.listeners)
	return strings.Join(lines, "\n")
}

// localNetworkDenied reports Local Network privacy's signature: no dial to the container's own
// address reached it, at least one failed EHOSTUNREACH, and the Mac holds a LIVE route to it on
// a directly attached interface (acRoute.live). A missing route, or one through a gateway, is a
// different fault, and so is a refusal: the policy's answer is specifically EHOSTUNREACH, before
// any packet leaves the Mac.
func (e acMacEvidence) localNetworkDenied() bool {
	if !e.route.live {
		return false
	}
	dialed, unreachable := false, false
	for _, p := range e.probes {
		if !p.container {
			continue
		}
		if p.result.reached {
			return false
		}
		for _, d := range p.result.dials {
			dialed = true
			unreachable = unreachable || d.kind == acKindNoRoute
		}
	}
	return dialed && unreachable
}

// acPortMacDiag waits for the jail's address file, then gathers the Mac-side evidence
// acPortResult.mac describes. Every dial is bounded (acPortMacTries), so it adds seconds, not
// the launch's whole bound, and it gives up with a line saying so if the file never comes.
func acPortMacDiag(stop <-chan struct{}, clock acProbeClock, v4Host, dualHost int, v4Token, dualToken string) acMacEvidence {
	addrFile := filepath.Join(clock.dir, acPortAddrFile)
	var fields []string
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(500 * time.Millisecond) {
		if b, err := os.ReadFile(addrFile); err == nil {
			if fields = strings.Fields(string(b)); len(fields) == 3 {
				break
			}
		}
		select {
		case <-stop:
			return acMacEvidence{missing: "the jail exited before writing " + acPortAddrFile + "; no Mac-side evidence"}
		default:
		}
		if time.Now().After(deadline) {
			return acMacEvidence{missing: "the jail never wrote " + acPortAddrFile + " within 60s; no Mac-side evidence"}
		}
	}
	jailIP, gw, name := fields[0], fields[1], fields[2]
	type target struct {
		label, host string
		container   bool
		port        int
		token       string
	}
	targets := []target{
		{"container address, IPv4-only listener", jailIP, true, acPublishedPortJailPort, v4Token},
		{"container address, dual-stack listener", jailIP, true, acPublishedPortDualJailPort, dualToken},
		{"published port on the vmnet gateway, IPv4-only", gw, false, v4Host, v4Token},
		{"published port on the vmnet gateway, dual-stack", gw, false, dualHost, dualToken},
		{"published port on [::1], IPv4-only", "::1", false, v4Host, v4Token},
		{"published port on [::1], dual-stack", "::1", false, dualHost, dualToken},
	}
	// Concurrently, and each into its own slot, so the whole diagnostic costs one probe's
	// worst case rather than six in a row, and still prints in this fixed order.
	probes := make([]acMacProbe, len(targets))
	var wg sync.WaitGroup
	for i, tg := range targets {
		probes[i] = acMacProbe{label: tg.label, container: tg.container}
		if tg.host == "" || tg.host == "-" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := net.JoinHostPort(tg.host, strconv.Itoa(tg.port))
			probes[i].result = acPollListener(stop, addr, tg.token, clock, time.Second, acPortMacTries)
		}()
	}
	wg.Wait()
	return acMacEvidence{
		probes:    probes,
		route:     acPortRoute(jailIP),
		inspect:   acPortInspect(name),
		listeners: acPortListeners(v4Host, dualHost),
	}
}

// acPortInspect is what `container inspect` says about the container's published ports and
// networks, and only that: the whole document runs to kilobytes of init-process argv, and
// the third Mac run (2026-09-25) cut it off before either field.
func acPortInspect(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "container", "inspect", name).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("container inspect %s: %v: %s", name, err, lastLines(string(out), 5))
	}
	var docs []map[string]any
	if err := json.Unmarshal(out, &docs); err != nil || len(docs) == 0 {
		return fmt.Sprintf("container inspect %s: unparseable (%v): %.400s", name, err, out)
	}
	pick := map[string]any{}
	if cfg, ok := docs[0]["configuration"].(map[string]any); ok {
		pick["configuration.publishedPorts"] = cfg["publishedPorts"]
		pick["configuration.networks"] = cfg["networks"]
	}
	pick["networks"] = docs[0]["networks"]
	pick["status"] = docs[0]["status"]
	b, _ := json.MarshalIndent(pick, "", "  ")
	return fmt.Sprintf("container inspect %s, published ports and networks:\n%s", name, b)
}

// acRoute is the Mac kernel's route to the container, as `route -n get` prints it.
type acRoute struct {
	dest  string
	text  string // the command and its output, for the record
	iface string
	// live is a route that exists and delivers directly: the command succeeded, names an
	// interface, and its flags carry UP and not GATEWAY. On macOS every address has SOME route
	// once a default route exists, so "a route exists" alone would say nothing; a directly
	// attached one (bridge100 for the vmnet subnet) is what makes an EHOSTUNREACH a policy on
	// this process rather than a missing path.
	live bool
}

// acPortRoute asks the Mac kernel for its route to the container. The third Mac run's direct
// dials failed "no route to host" on the directly attached vmnet subnet, and a hand-run
// `route -n get` named bridge100 throughout: that pairing is what measured Local Network
// privacy (acMacEvidence.localNetworkDenied).
func acPortRoute(jailIP string) acRoute {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "route", "-n", "get", jailIP).CombinedOutput()
	return acParseRoute(jailIP, string(out), err)
}

// acParseRoute reads `route -n get`'s "key: value" lines (macOS's route(8) format).
func acParseRoute(dest, out string, err error) acRoute {
	r := acRoute{dest: dest, text: fmt.Sprintf("route -n get %s (err=%v):\n%s", dest, err, strings.TrimSpace(out))}
	if err != nil {
		return r
	}
	var flags []string
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "interface":
			r.iface = strings.TrimSpace(val)
		case "flags":
			flags = strings.Split(strings.Trim(strings.TrimSpace(val), "<>"), ",")
		}
	}
	up, gateway := false, false
	for _, f := range flags {
		up = up || f == "UP"
		gateway = gateway || f == "GATEWAY"
	}
	r.live = r.iface != "" && up && !gateway
	return r
}

// acPortListeners names the Mac process holding each published host port and the address it
// bound, which the dials can only infer (the third run found the port accepting on the vmnet
// gateway and refused on loopback).
func acPortListeners(ports ...int) string {
	args := []string{"-nP", "-sTCP:LISTEN"}
	for _, p := range ports {
		args = append(args, "-iTCP:"+strconv.Itoa(p))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", args...).CombinedOutput()
	return fmt.Sprintf("lsof %s (err=%v):\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
}

// acPortMacTries bounds each Mac-side diagnostic target's dials: the jail is already listening
// when its address file appears, so a path that works answers on the first try or two.
const acPortMacTries = 5

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

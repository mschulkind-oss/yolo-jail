package run

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/internaldaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/journald"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// TestMain lets a self-exec'd `yolo internal daemon <name>` spawn resolve to THIS test
// binary and actually run that daemon. execx.SelfExecArgv rewrites the leading "yolo" token
// to os.Executable() — the test binary here — so a launch that starts any shipped host
// daemon re-execs this binary with that argv, and the tests that drive the REAL records
// (the journal bridge, host-processes and the two credential brokers behind their fronts)
// depend on the child serving it.
//
// EVERY NAME, THROUGH THE PRODUCTION TABLE (internaldaemon.Run), never a list kept here. This
// dispatch used to name four daemons one by one; a launch that started any other one (aws-auth,
// for a macos-user launch that enables it) re-ran this whole suite in a detached child that
// shared this process's host-singleton dir and killed its daemons.
// TestNoShippedDaemonSelfExecRunsTheSuite says what that cost and pins the fix.
func TestMain(m *testing.M) {
	if len(os.Args) >= 2 && os.Args[1] == "-aws-auth-refusal-child" {
		os.Exit(awsauthdaemon.Main(os.Args[2:]))
	}
	if internaldaemon.IsDaemonArgv(os.Args) {
		os.Exit(internaldaemon.Run(os.Args[3:]))
	}
	// `<test-binary> -front-upstream-child <mode> <socket>` is the daemon child
	// for the publishes:"socket" tests: it binds a REAL AF_UNIX socket, which no
	// portable sh one-liner can (precedent:
	// internal/oauthbroker/singletontransport_test.go's TestMain hook).
	if len(os.Args) >= 4 && os.Args[1] == "-front-upstream-child" {
		os.Exit(frontUpstreamChildMain(os.Args[2], os.Args[3]))
	}
	if len(os.Args) >= 4 && os.Args[1] == "-settings-check-child" {
		os.Exit(settingsCheckChildMain(os.Args[2], os.Args[3], "validator"))
	}
	if len(os.Args) >= 4 && os.Args[1] == "-settings-rewrite-check-child" {
		os.Exit(settingsRewriteCheckChildMain(os.Args[2], os.Args[3]))
	}
	if len(os.Args) >= 3 && os.Args[1] == "-settings-refusal-child" {
		os.Exit(settingsRefusalChildMain(os.Args[2]))
	}
	if len(os.Args) >= 5 && os.Args[1] == "-settings-interleave-child" {
		os.Exit(settingsInterleaveDaemonChildMain(os.Args[2], os.Args[3], os.Args[4]))
	}
	if len(os.Args) >= 5 && os.Args[1] == "-settings-snapshot-daemon" {
		os.Exit(settingsSnapshotDaemonChildMain(os.Args[2], os.Args[3], os.Args[4]))
	}
	// `<test-binary> -splice-child <socket> <upstream>` is a spawned per-jail daemon that relays
	// every connection to a handler the test serves in its own process (hostdaemonperjail_test.go),
	// so a per-jail spawn reaches the real handler a fixture stands up.
	if len(os.Args) >= 4 && os.Args[1] == "-splice-child" {
		os.Exit(spliceChildMain(os.Args[2], os.Args[3]))
	}
	// `<test-binary> -bind-probe-child <host:port>` binds the address once, as a jail daemon does
	// at boot, and says whether it could (keeperreservedports_test.go).
	if len(os.Args) >= 3 && os.Args[1] == "-bind-probe-child" {
		os.Exit(bindProbeChildMain(os.Args[2]))
	}
	// THE PODMAN READINESS GATE REFUSES BY DEFAULT HERE (podmanready.go). Its real attempt
	// runner starts `podman info` on whatever PATH the test process has, and a test that
	// reached the gate without a fake would then pass or fail on the machine's podman. A
	// test that goes through runtime selection says what podman answers
	// (Options.PodmanReadiness, answeringPodman).
	defaultPodmanAttempt = refusingPodmanAttempt
	// A FRESH LAUNCH'S KEEPER RUNS IN-PROCESS HERE (keeper_test.go): the real spawner self-execs
	// the binary, which here is the test binary, and it refuses to (errTestBinarySelfExec).
	defaultKeeperSpawner = inProcessKeeper
	// A fixture's git reads no machine configuration (testsupport.HermeticGitEnv), and the
	// tripwire makes one that does fail here and on CI, not only on a machine that signs.
	testsupport.ArmGitConfigTripwire()
	// A JAIL'S SETUP NEVER CHOOSES A TEST'S. This repo's jail exports YOLO_RUNTIME (removed: it
	// refuses every host launch) and may export YOLO_SETUP; fixtures that build their environment
	// from os.Environ would select the outer jail's setup instead of a host's
	// (TestThisPackageClearsTheSetupSelectionEnv).
	testsupport.ClearSetupSelectionEnv()
	// Past every daemon dispatch above, so a re-exec'd child neither redirects nor stops
	// anything: this package's launches spawn real host singletons, which get a private
	// directory instead of the machine-wide /tmp/yolo-<name>.* (testsupport says why), and
	// the ones they started are stopped when the package ends.
	release := testsupport.IsolateHostSingletons()
	// EVERY TEST STARTS IN A HOME OF ITS OWN. Most fixtures set HOME; the host-service ones
	// (startExternalServiceHarness and its neighbors) did not, and each wrote its log into the
	// real ~/.local/share/yolo-jail/logs, beside the shipped host-processes and journal
	// loopholes' own logs, which two of them appended to.
	releaseHome := testsupport.IsolateHome()
	// THE PIN EVERY FIXTURE'S OPTIONS CARRY (pinnedhelpers_test.go): a launch execs its helpers from
	// the pinned host yolo and never from os.Executable(), so a fixture that execs one is given a
	// pin whose file is this test binary.
	releasePin := installSharedTestPin()
	code := m.Run()
	// EVERY SIGNAL ARM STOPS THE PROCESS'S NIX (launchSignalArm.terminate), and that stop is
	// permanent: a test that drove an arm to its exit on the process's own set refused every later
	// test's tracked nix, so the order the tests ran in decided whether those passed. A test that
	// fires an arm gives itself a set of its own (nixchildren.Isolate; seeArmExitsWith does).
	if nixchildren.Stopped() {
		fmt.Fprintln(os.Stderr, "FAIL: a test stopped this process's own set of tracked nix children: "+
			"give each test that drives a signal arm to its exit nixchildren.Isolate(t)")
		if code == 0 {
			code = 1
		}
	}
	releasePin()
	releaseHome()
	release()
	os.Exit(code)
}

// TestThisPackageStartsInAHomeOfItsOwn fails if TestMain stops isolating HOME, which would put
// every fixture that forgets to set its own back to writing under the real home.
func TestThisPackageStartsInAHomeOfItsOwn(t *testing.T) {
	home := testsupport.IsolatedHome()
	if home == "" || os.Getenv("HOME") != home {
		t.Errorf("HOME = %q at a test's start, want the home TestMain isolated (%q)", os.Getenv("HOME"), home)
	}
}

// TestThisPackageClearsTheSetupSelectionEnv fails if TestMain stops clearing the variables a
// jail exports that select a setup, which would make every launch fixture here select the outer
// jail's setup, or refuse on its removed YOLO_RUNTIME, wherever the suite runs inside a jail.
func TestThisPackageClearsTheSetupSelectionEnv(t *testing.T) {
	for _, name := range testsupport.SetupSelectionEnv {
		if v, ok := os.LookupEnv(name); ok {
			t.Errorf("%s = %q at a test's start, want TestMain to have cleared it", name, v)
		}
	}
}

// TestShippedJournalPackRunsBehindTheFront is what replaced TestResolveJournalMode,
// TestStartJournalStartsBridge and TestStartJournalSkipsWhenOff — and the deletion is
// the subject rather than a side effect.
//
// Those three tested a BUILTIN SERVICE: a top-level `journal` config key normalized
// to a `--mode` argv by resolveJournalMode, handed to a bespoke startJournal step
// that stood beside the cgroup delegate in startLoopholes. All of that is gone
// (docs/reference/loophole-system.md OQ-A6, pack-config-keys.md OQ-K4). There is no
// journal step, no journal key and no journal mode resolver to test, because the
// bridge is now an ordinary manifest loophole discovered from the official `journal`
// pack and spawned by the same loop as every other host daemon. What is left to pin
// is that the SHIPPED manifest actually works — which the old tests never asked,
// because there was no manifest.
//
// So this drives the real record: DISCOVERED through the pack loader (hence through
// the pack-shipped subset), spawned with the manifest's own argv, dialed over the
// real front by the real client path.
//
// THE CLAIM IT PINS is "nothing jail-facing moves". The daemon stopped publishing its
// own endpoint and now binds a plain socket behind yolo's front, but the env var, the
// in-jail path and the endpoint leaf in the mounted services dir are functions of the
// loophole NAME and the transport alone — so cmd/yolo-journalctl needed no change, and
// this says so with a running daemon rather than by reading loopholesruntime.go.
func TestShippedJournalPackRunsBehindTheFront(t *testing.T) {
	// The bridge forwards `journalctl`, and the manifest declares platforms: ["linux"]
	// for that reason. On macOS the self-exec'd daemon could not bind under $TMPDIR's
	// long sun_path anyway.
	if runtime.GOOS != "linux" {
		t.Skip("the journal bridge is Linux-only (a journalctl forwarder)")
	}
	// The manifest's argv names {settings}, which resolves at RECORD LOAD to a file
	// under the loophole's state dir — so the redirect has to be in place before
	// Discover runs, or the record bakes in a path under the real ~/.local/share and
	// this test writes its mode somewhere the daemon is not reading.
	stateRoot := t.TempDir()
	realStateDir := loopholes.StateDirFor
	loopholes.StateDirFor = func(name string) string { return filepath.Join(stateRoot, name) }
	t.Cleanup(func() { loopholes.StateDirFor = realStateDir })

	var lp *loopholes.Loophole
	for _, cand := range loopholes.Discover(loopholes.DiscoverOptions{
		IncludeDisabled: true,
		PackModules:     []loopholes.PackModule{shippedPackLoopholeModule(t, "journal")},
	}) {
		if cand.Name == "journal" {
			lp = cand
		}
	}
	if lp == nil || lp.HostDaemon == nil {
		t.Fatal("the shipped journal loophole did not discover with a host_daemon")
	}
	if lp.Source != loopholes.SourcePack {
		t.Errorf("Source = %q, want %q — a `journal` record from anywhere else means the "+
			"builtin is back", lp.Source, loopholes.SourcePack)
	}
	if lp.HostDaemon.Publishes != loopholes.PublishesSocket {
		t.Fatalf("publishes = %q, want %q — a pack-shipped loophole may not self-publish, "+
			"so this is the manifest failing the subset rather than a preference",
			lp.HostDaemon.Publishes, loopholes.PublishesSocket)
	}
	if !lp.HostDaemon.Preamble {
		t.Fatal("preamble = false — the manifest declares nothing, so the decoder's default " +
			"ON must survive internal/loopholes' field-by-field resolve, and journald's " +
			"ServeFrontedUnix is written to consume the frame")
	}

	// THE MODE ARRIVES THROUGH THE GRANT FILE, written by the same launch-path function
	// the run pipeline calls, from the standings the config-change gate approved. Going
	// through writeLoopholeSettings rather than hand-writing the JSON is deliberate: the
	// manifest's [[grants]], the launch's grant block, the snapshot writer, the {settings}
	// substitution and the daemon's reader have to agree on one file, and only an
	// end-to-end path proves they do. `read_all` granted is the full mode (PD-11).
	cfg := jsonx.NewOrderedMap()
	o := &Options{}
	fillDefaults(o)
	o.helperPin = testPin()
	o.approvedLoopholeGrants = map[string]map[string]config.LoopholeGrantStanding{
		"journal": {
			"read":     {Standing: string(loopholegrant.Requestable)},
			"read_all": {Standing: string(loopholegrant.Granted)},
		},
	}

	// A fake journalctl that prints its argv, so the assertion below can read the mode
	// the daemon resolved OFF THE WIRE rather than off a log line.
	//
	// `printf`, not `echo "$@"`: the request below sends `-n 5`, and echo EATS a
	// leading -n as its own flag — which silently turns the argv assertion into an
	// assertion about the string "5".
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "journalctl"),
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	env := jsonx.NewOrderedMap()
	env.Set("PATH", binDir+":"+os.Getenv("PATH"))
	spec := jsonx.NewOrderedMap()
	spec.Set("command", toAnyList(lp.HostDaemon.Cmd))
	spec.Set("env", env)

	var buf strings.Builder
	o.Stdout = &buf
	o.writeLoopholeSettings([]*loopholes.Loophole{lp}, cfg)
	snapshot := o.settingsSnapshots["journal"]
	if snapshot == nil {
		t.Fatalf("no grant file for the journal, which declares no settings but does declare "+
			"grants; output: %q", buf.String())
	}
	defer o.cleanupSettingsSnapshots()
	if _, block, err := loopholegrant.Read(snapshot.path); err != nil || block == nil ||
		block.Kinds["read_all"].Standing != loopholegrant.Granted || block.Access != "read the host's journal" {
		t.Fatalf("grant file %s: block %+v, err %v; want read_all granted and the manifest's access",
			snapshot.path, block, err)
	}

	h, ok := o.startExternalService("journal", spec, socketsDir,
		lp.Transport, "127.0.0.1", lp.HostDaemon)
	if !ok {
		t.Fatalf("the shipped journal daemon failed to come up; output: %q", buf.String())
	}
	defer h.stop()

	// Nothing jail-facing moved.
	if h.envVarName != "YOLO_SERVICE_JOURNAL_ENDPOINT" {
		t.Errorf("envVarName = %q, want YOLO_SERVICE_JOURNAL_ENDPOINT", h.envVarName)
	}
	if h.jailPath != "/run/yolo-services/journal.endpoint" {
		t.Errorf("jailPath = %q, want /run/yolo-services/journal.endpoint", h.jailPath)
	}
	wantEndpoint := filepath.Join(socketsDir, "journal.endpoint")
	if h.hostPath != wantEndpoint {
		t.Errorf("hostPath = %q, want %q", h.hostPath, wantEndpoint)
	}
	// Probe, not existence: a truncated or older-format file would otherwise read as
	// healthy forever, so the daemon would never be respawned and the jail could never
	// reach it.
	if !svcendpoint.Probe(wantEndpoint) {
		t.Fatalf("journal endpoint %q was never published in a usable form", wantEndpoint)
	}
	if fi, err := os.Stat(wantEndpoint); err != nil {
		t.Errorf("stat endpoint: %v", err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("endpoint mode = %04o, want 0600 — the file carries this jail's bearer token",
			fi.Mode().Perm())
	}

	// One real round trip through the front, which is what proves the manifest's
	// preamble default and the daemon's preamble read are the same decision. If they
	// were not, the daemon would parse yolo's preamble frame as the request header and
	// answer "malformed request", exit 2.
	out, rc := dialJournal(t, wantEndpoint, `{"args":["-n","5"]}`)
	if rc != 0 {
		t.Fatalf("rc=%d, want 0 (stdout %q) — a non-zero exit here is the daemon reading "+
			"the connection preamble AS the request", rc, out)
	}
	// `read_all` granted means the client's args pass through unchanged. The narrow mode
	// would have prepended --user, so this one assertion covers the whole chain:
	// declaration → gate standing → grant file → daemon → journalctl argv.
	if strings.Contains(out, "--user") {
		t.Errorf("journalctl argv = %q; read_all granted must NOT prepend --user, or the "+
			"grant the gate approved never reached the daemon", out)
	}
	if !strings.Contains(out, "-n 5") {
		t.Errorf("journalctl argv = %q, want the client's own args", out)
	}
}

// dialJournal speaks the journal bridge's wire protocol over the published endpoint:
// the jail's real dial path (read the file, pin that cert, present that token), then
// a newline-terminated JSON request, then ">BI" frames back. Returns accumulated
// stdout and the exit code.
func dialJournal(t *testing.T, endpoint, request string) (string, int) {
	t.Helper()
	conn, err := svcendpoint.DialLocal(endpoint, 5*time.Second)
	if err != nil {
		t.Fatalf("dialing %s: %v", endpoint, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte(request + "\n")); err != nil {
		t.Fatal(err)
	}
	return readJournalFrames(t, conn)
}

func readJournalFrames(t *testing.T, c net.Conn) (string, int) {
	t.Helper()
	var stdout strings.Builder
	for {
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(c, hdr); err != nil {
			return stdout.String(), -999 // EOF before an exit frame
		}
		payload := make([]byte, binary.BigEndian.Uint32(hdr[1:]))
		if len(payload) > 0 {
			if _, err := io.ReadFull(c, payload); err != nil {
				return stdout.String(), -999
			}
		}
		switch hdr[0] {
		case journald.FrameStdout:
			stdout.Write(payload)
		case journald.FrameExit:
			return stdout.String(), int(int32(binary.BigEndian.Uint32(payload)))
		}
	}
}

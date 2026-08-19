package run

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostprocesses"
	"github.com/mschulkind-oss/yolo-jail/internal/journald"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestMain lets a self-exec'd `yolo internal daemon journal` spawn resolve to THIS
// test binary and actually run the journal daemon, so the socket-bind wait succeeds
// in-process. execx.SelfExecArgv rewrites the leading "yolo" token to
// os.Executable() (the test binary here), so without this dispatch the spawn would
// re-exec the test binary, which would ignore the args and never bind the socket.
func TestMain(m *testing.M) {
	if len(os.Args) >= 4 && os.Args[1] == "internal" && os.Args[2] == "daemon" && os.Args[3] == "journal" {
		os.Exit(journald.Main(os.Args[4:]))
	}
	// The same dispatch for the host-processes daemon, so
	// TestBundledHostProcessesRunsBehindTheFront can drive the REAL shipped
	// record — argv and all — instead of a stand-in: its manifest cmd is
	// ["yolo","internal","daemon","host-processes","--socket","{socket}",…], and
	// SelfExecArgv rewrites that leading "yolo" to this test binary.
	if len(os.Args) >= 4 && os.Args[1] == "internal" && os.Args[2] == "daemon" &&
		os.Args[3] == "host-processes" {
		os.Exit(hostprocesses.Main(os.Args[4:]))
	}
	// And for the OAuth broker, so TestHostScopedBrokerDaemonAnswersThroughTheFront
	// drives the REAL daemon — the one whose ServeUnix→ServeFrontedUnix move is the
	// half of the broker conversion no manifest assertion can see.
	if len(os.Args) >= 4 && os.Args[1] == "internal" && os.Args[2] == "daemon" &&
		os.Args[3] == "claude-oauth-broker" {
		os.Exit(oauthbroker.Main(os.Args[4:]))
	}
	// `<test-binary> -front-upstream-child <mode> <socket>` is the daemon child
	// for the publishes:"socket" tests: it binds a REAL AF_UNIX socket, which no
	// portable sh one-liner can (precedent:
	// internal/oauthbroker/singletontransport_test.go's TestMain hook).
	if len(os.Args) >= 4 && os.Args[1] == "-front-upstream-child" {
		os.Exit(frontUpstreamChildMain(os.Args[2], os.Args[3]))
	}
	os.Exit(m.Run())
}

// TestShippedJournalPackRunsBehindTheFront is what replaced TestResolveJournalMode,
// TestStartJournalStartsBridge and TestStartJournalSkipsWhenOff — and the deletion is
// the subject rather than a side effect.
//
// Those three tested a BUILTIN SERVICE: a top-level `journal` config key normalized
// to a `--mode` argv by resolveJournalMode, handed to a bespoke startJournal step
// that stood beside the cgroup delegate in startLoopholes. All of that is gone
// (docs/design/loophole-activation.md OQ-A6, pack-config-keys.md OQ-K4). There is no
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

	// THE MODE ARRIVES THROUGH THE SETTINGS FILE, written by the same launch-path
	// function the run pipeline calls. Going through writeLoopholeSettings rather than
	// hand-writing the JSON is deliberate: the manifest declaration, the resolver, the
	// writer and the daemon's reader are four pieces that have to agree on one file,
	// and only an end-to-end path proves they do.
	cfg := jsonx.NewOrderedMap()
	loopBlock := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	settings := jsonx.NewOrderedMap()
	settings.Set("full", true)
	entry.Set("settings", settings)
	loopBlock.Set("journal", entry)
	cfg.Set("loopholes", loopBlock)

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

	o := &Options{}
	fillDefaults(o)
	var buf strings.Builder
	o.Stdout = &buf
	o.writeLoopholeSettings([]*loopholes.Loophole{lp}, cfg)
	if !fileExists(loopholes.SettingsFileFor("journal")) {
		t.Fatalf("no settings file at %s; output: %q",
			loopholes.SettingsFileFor("journal"), buf.String())
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
	// `full: true` means the client's args pass through unchanged. The narrow mode
	// would have prepended --user, so this one assertion covers the whole chain:
	// declaration → config value → settings file → daemon → journalctl argv.
	if strings.Contains(out, "--user") {
		t.Errorf("journalctl argv = %q; settings.full=true must NOT prepend --user, or the "+
			"escalation the user config asked for never reached the daemon", out)
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

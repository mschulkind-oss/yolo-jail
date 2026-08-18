package check

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// privateDir returns a 0700 dir. t.TempDir() creates 0755, which svcendpoint
// correctly REFUSES to publish a credential into.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "svc")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func probeOnce(t *testing.T, endpointPath string) (*reporter, string) {
	t.Helper()
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{}
	fillDefaults(o)
	o.checkLoopbackTLSService(r, "loophole svc @ jail", endpointPath, "svc")
	return r, buf.String()
}

// TestCheckLoopbackTLSServiceNamesTheLayer: the host-side prober must distinguish
// the three faults, because they have three different fixes.
//
// The prober also has to AUTHENTICATE, not merely stat a path — otherwise it reports
// a dead daemon as healthy. That it can authenticate at all is a consequence of
// putting the token in the endpoint file: the prober reads the same 0600 file as the
// same uid that published it.
func TestCheckLoopbackTLSServiceNamesTheLayer(t *testing.T) {
	dir := privateDir(t)

	t.Run("missing", func(t *testing.T) {
		r, out := probeOnce(t, filepath.Join(dir, "absent.endpoint"))
		if r.failed != 1 || !strings.Contains(out, "no endpoint published") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("incomplete", func(t *testing.T) {
		p := filepath.Join(dir, "partial.endpoint")
		// Two fields: an older publication, and also what a torn write looks like.
		if err := os.WriteFile(p, []byte("127.0.0.1:1 Y29zdA==\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r, out := probeOnce(t, p)
		if r.failed != 1 || !strings.Contains(out, "endpoint file incomplete") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("complete but dead", func(t *testing.T) {
		p := filepath.Join(dir, "dead.endpoint")
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		ep, err := svcendpoint.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		_ = ln.Close() // Close unlinks; republish the same bytes as a crashed daemon would leave.
		if err := svcendpoint.Publish(p, ep); err != nil {
			t.Fatal(err)
		}
		r, out := probeOnce(t, p)
		if r.failed != 1 || !strings.Contains(out, "listener unreachable") {
			t.Errorf("a complete file naming a DEAD listener passed: failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("auth rejected", func(t *testing.T) {
		p := filepath.Join(dir, "live.endpoint")
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		ep, err := svcendpoint.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		ep.Token = strings.Repeat("a", len(ep.Token))
		bad := filepath.Join(dir, "wrongtoken.endpoint")
		if err := svcendpoint.Publish(bad, ep); err != nil {
			t.Fatal(err)
		}
		r, out := probeOnce(t, bad)
		if r.failed != 1 || !strings.Contains(out, "rejected the token") {
			t.Errorf("a wrong token was not attributed to auth: failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("healthy", func(t *testing.T) {
		p := filepath.Join(dir, "ok.endpoint")
		// The DEFAULT advertise host on purpose: the gateway name, exactly as a real
		// daemon publishes it. DialLocal keeping the port and substituting 127.0.0.1
		// is the whole reason a host-side prober works at all, so a test that
		// published 127.0.0.1 would prove nothing.
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()
		r, out := probeOnce(t, p)
		if r.failed != 0 || r.passed != 1 || !strings.Contains(out, "endpoint accepting") {
			t.Errorf("a live listener did not pass: passed=%d failed=%d out=%q", r.passed, r.failed, out)
		}
		// The green must not read as more than it is. DialLocal substituted
		// 127.0.0.1, so this says the daemon answers on the HOST's loopback and
		// nothing whatever about a jail — see hostSideProbeCaveat.
		if !strings.Contains(out, "host-side") || !strings.Contains(out, "in-jail reachability") {
			t.Errorf("a green host-side probe did not label itself: out=%q", out)
		}
	})
}

// TestCheckLoopholesWarnsOnWorkspaceDisable: §4.3b of loophole-packaging.md
// leaves `enabled` writable at workspace scope, so the DISCLOSURE is the only
// protection left for a default-on loophole: a workspace-sourced disable must
// WARN and name the file, never render as a green "disabled" line. A disable
// from the loophole's own manifest stays a green ok.
func TestCheckLoopholesWarnsOnWorkspaceDisable(t *testing.T) {
	// Isolate discovery: a FAKE bundled dir holding this test's two manifests, so no
	// real bundled loophole's doctor_cmd can ever run from a test. (It used to be an
	// empty bundled dir plus a hand-placed user dir; that channel is retired — OQ-LP10.)
	fakeBundled := t.TempDir()
	oldBundled := loopholes.BundledLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return fakeBundled }
	t.Cleanup(func() { loopholes.BundledLoopholesDir = oldBundled })
	retiredLoopholeDir(t) // absent, so the migration row cannot add a second warning
	writeManifest := func(name, body string) {
		dir := filepath.Join(fakeBundled, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest("wsoff", `{"name": "wsoff", "transport": "none", "default_enabled": true}`)
	writeManifest("selfoff", `{"name": "selfoff", "transport": "none", "default_enabled": false}`)

	ws := t.TempDir()
	wsCfg := filepath.Join(ws, "yolo-jail.jsonc")
	if err := os.WriteFile(wsCfg, []byte(`{"loopholes": {"wsoff": {"enabled": false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{Workspace: ws, Getenv: func(string) string { return "" }}
	fillDefaults(o)
	o.checkLoopholes(r)
	out := buf.String()

	if r.warned != 1 || !strings.Contains(out, "wsoff") ||
		!strings.Contains(out, "disabled by "+wsCfg) {
		t.Errorf("warned=%d out=%q — a workspace-scope disable must WARN and name the file",
			r.warned, out)
	}
	// The manifest's own disable stays a green ok line.
	if !strings.Contains(out, "loophole selfoff: disabled") {
		t.Errorf("out=%q — a manifest-level disable should still render", out)
	}
	if r.failed != 0 {
		t.Errorf("failed=%d out=%q — disclosures are warnings, not failures", r.failed, out)
	}
}

// brokerRelayProbeOnce runs the broker-relay probe with the in-jail visibility
// exec stubbed out (rt/cname empty => the tri-state probe returns unknown, which
// the caller treats as "don't second-guess the host-side answer").
func brokerRelayProbeOnce(t *testing.T, endpointPath string) (*reporter, string) {
	t.Helper()
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{}
	fillDefaults(o)
	o.checkBrokerRelay(r, "loophole claude-oauth-broker @ jail", endpointPath, "", "")
	return r, buf.String()
}

// TestCheckBrokerRelayProbesTheHopTheJailUses: the relay probe must go through the
// endpoint file — pin, token, then ping — not through the relay's own socket.
//
// That socket is host-only now, so probing it would test a path no jail travels: it
// can be perfectly healthy while the jail's half is unpublished, stale or
// mismatched, which is exactly the outage this probe exists to name. The probe
// authenticates as the same uid that published the file, which is possible only
// because the token lives in the file rather than in the jail's environment.
func TestCheckBrokerRelayProbesTheHopTheJailUses(t *testing.T) {
	dir := privateDir(t)

	t.Run("endpoint missing", func(t *testing.T) {
		r, out := brokerRelayProbeOnce(t, filepath.Join(dir, "absent.endpoint"))
		if r.failed != 1 || !strings.Contains(out, "relay endpoint missing") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("endpoint incomplete", func(t *testing.T) {
		p := filepath.Join(dir, "relay-partial.endpoint")
		if err := os.WriteFile(p, []byte("127.0.0.1:1 Y29zdA==\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r, out := brokerRelayProbeOnce(t, p)
		if r.failed != 1 || !strings.Contains(out, "relay endpoint incomplete") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("auth rejected is its own message", func(t *testing.T) {
		p := filepath.Join(dir, "relay-live.endpoint")
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		ep, err := svcendpoint.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		ep.Token = strings.Repeat("b", len(ep.Token))
		bad := filepath.Join(dir, "relay-wrongtoken.endpoint")
		if err := svcendpoint.Publish(bad, ep); err != nil {
			t.Fatal(err)
		}
		r, out := brokerRelayProbeOnce(t, bad)
		if r.failed != 1 || !strings.Contains(out, "rejected this jail's token") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
		// It must NOT be reported as the broker failing behind a working relay.
		if strings.Contains(out, "broker unreachable") {
			t.Errorf("a token mismatch was blamed on the broker: %q", out)
		}
	})

	t.Run("relay authenticates but broker does not answer", func(t *testing.T) {
		p := filepath.Join(dir, "relay-nobroker.endpoint")
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close() // authenticated, then nothing: the broker behind it is down
			}
		}()
		r, out := brokerRelayProbeOnce(t, p)
		if r.failed != 1 || !strings.Contains(out, "broker unreachable") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("healthy end to end", func(t *testing.T) {
		p := filepath.Join(dir, "relay-ok.endpoint")
		// The DEFAULT advertise host, as a real relay publishes it — DialLocal
		// substituting 127.0.0.1 for the gateway name is what makes a host-side probe
		// possible at all.
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go serveBrokerPong(ln)
		r, out := brokerRelayProbeOnce(t, p)
		if r.failed != 0 || r.passed != 1 || !strings.Contains(out, "broker answers through it") {
			t.Errorf("a live relay with a live broker did not pass: passed=%d failed=%d out=%q",
				r.passed, r.failed, out)
		}
		if !strings.Contains(out, "token-authenticated") {
			t.Errorf("the success line does not record that the probe authenticated: %q", out)
		}
		// Same rule as the loopback-TLS probe's green: the relay was dialled on
		// 127.0.0.1, so the line may not imply a jail can reach it.
		if !strings.Contains(out, "host-side") || !strings.Contains(out, "in-jail reachability") {
			t.Errorf("a green host-side relay probe did not label itself: out=%q", out)
		}
	})
}

// serveBrokerPong answers the check's framed {"action":"ping"} with {"pong":true}
// + exit 0, standing in for the singleton behind the relay.
//
// The preamble read is NOT optional bookkeeping, and skipping it is the SILENT
// half of this change: a double that read one framed message and answered would
// still answer — it would just be answering yolo's connection preamble instead of
// the probe's ping, and every assertion here would stay green while the double
// stopped resembling anything. Consuming it is what keeps "the check's ping
// reached a daemon" the fact this test reports.
func serveBrokerPong(ln *svcendpoint.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			if _, err := svcendpoint.ReadPreamble(c); err != nil {
				return
			}
			hdr := make([]byte, 4)
			if _, err := io.ReadFull(c, hdr); err != nil {
				return
			}
			req := make([]byte, binary.BigEndian.Uint32(hdr))
			if _, err := io.ReadFull(c, req); err != nil {
				return
			}
			// ANSWER ONLY A REAL PING. Without this the double would pong at
			// whatever arrived first, which is precisely how the preamble bug
			// above would have hidden: a hung-up connection makes the probe fail
			// and the caller's assertion say so, out loud.
			if !bytes.Contains(req, []byte(`"action"`)) {
				return
			}
			body := []byte(`{"pong": true}`)
			fh := make([]byte, 5)
			binary.BigEndian.PutUint32(fh[1:], uint32(len(body)))
			_, _ = c.Write(fh) // stream 0
			_, _ = c.Write(body)
			ex := make([]byte, 5)
			ex[0] = 2
			binary.BigEndian.PutUint32(ex[1:], 4)
			_, _ = c.Write(ex)
			_, _ = c.Write([]byte{0, 0, 0, 0})
		}(c)
	}
}

// TestJailEndpointProbeUsesTestF: the in-jail visibility probe must test a regular
// FILE. `test -S` would report every healthy jail as broken, because what crosses
// into the jail is an endpoint file, not a socket.
func TestJailEndpointProbeUsesTestF(t *testing.T) {
	var gotArgv []string
	o := &Options{}
	fillDefaults(o)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		gotArgv = argv
		return ExecResult{Ran: true, RC: 0}
	}
	v := o.relayEndpointVisibleInJail("podman", "yolo-ws-abcd1234")
	if v == nil || !*v {
		t.Fatalf("rc=0 should read as visible, got %v", v)
	}
	joined := strings.Join(gotArgv, " ")
	if !strings.Contains(joined, "test -f ") {
		t.Errorf("probe argv = %q, want a `test -f` on the endpoint file", joined)
	}
	if strings.Contains(joined, "test -S") {
		t.Errorf("probe still tests for a SOCKET: %q", joined)
	}
	if !strings.Contains(joined, "/run/yolo-services/claude-oauth-broker.endpoint") {
		t.Errorf("probe does not name the in-jail endpoint file: %q", joined)
	}
}

// retiredLoopholeDir points the RETIRED hand-placed loopholes dir (OQ-LP10) at a fresh
// temp dir and returns it. Empty by default, which is what an isolated test wants: a
// developer whose real home still holds one would otherwise get an extra warning row in
// every loophole test here.
func retiredLoopholeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := loopholes.RetiredUserLoopholesDir
	loopholes.RetiredUserLoopholesDir = func() string { return dir }
	t.Cleanup(func() { loopholes.RetiredUserLoopholesDir = orig })
	return dir
}

// TestCheckLoopholesReportsTheRetiredDirectory: whatever sat in the hand-placed
// loopholes directory was running a HOST DAEMON until the upgrade that retired the
// channel (OQ-LP10). `yolo check` is the command someone runs when a loophole stops
// working, so the migration has to be a graded row there — not only a stderr line from
// discovery, which scrolls past.
func TestCheckLoopholesReportsTheRetiredDirectory(t *testing.T) {
	fakeBundled := t.TempDir()
	oldBundled := loopholes.BundledLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return fakeBundled }
	t.Cleanup(func() { loopholes.BundledLoopholesDir = oldBundled })

	retired := retiredLoopholeDir(t)
	mod := filepath.Join(retired, "leftover")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"),
		[]byte(`{"name": "leftover", "transport": "none"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}
	fillDefaults(o)
	o.checkLoopholes(r)
	out := buf.String()

	if r.warned != 1 {
		t.Errorf("warned=%d, want 1 — a retired directory that still holds a loophole is a "+
			"capability that silently stopped, and silence is the failure mode here:\n%s",
			r.warned, out)
	}
	for _, want := range []string{"leftover", retired, "local"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
	if r.failed != 0 {
		t.Errorf("failed=%d — a migration notice is a warning, not a failure:\n%s", r.failed, out)
	}
	// And the manifest sitting there is NOT loaded: reporting it must not resurrect it.
	if strings.Contains(out, "loophole leftover:") {
		t.Errorf("the retired directory's manifest was walked as a live loophole:\n%s", out)
	}
}

// TestHostServiceLivenessSaysWhatItCannotSee pins the honesty of the section as a
// whole, which is a property of the OUTPUT rather than of any one dial.
//
// Both probes in it use svcendpoint.DialLocal, which keeps the published port and
// substitutes 127.0.0.1 — where the daemons bind, and the one address a jail cannot
// use. That is structural: `yolo check` runs host-side, and the advertised address is
// only meaningful inside a namespace the runtime built, so this section reported PASS
// through a total in-jail outage (docs/design/loopback-tls-reachability.md §7). It
// cannot be fixed by dialling differently, so what is pinned here is the wording: a
// green that labels itself, and the once-per-run pointer at the in-jail probe.
func TestHostServiceLivenessSaysWhatItCannotSee(t *testing.T) {
	fakeBundled := t.TempDir()
	oldBundled := loopholes.BundledLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return fakeBundled }
	t.Cleanup(func() { loopholes.BundledLoopholesDir = oldBundled })
	retiredLoopholeDir(t)

	modDir := filepath.Join(fakeBundled, "svc")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// host_daemon is what puts it in the "externals" set this section probes at all.
	if err := os.WriteFile(filepath.Join(modDir, "manifest.jsonc"), []byte(
		`{"name": "svc", "description": "x", "transport": "loopback-tls", `+
			`"default_enabled": true, "host_daemon": {"cmd": ["true"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The per-jail services directory is a deterministic function of the container
	// name, so a name unique to this test names a real directory no jail owns.
	const cname = "yolo-check-hostside-probe-test"
	svcDir := paths.HostServicesDir(cname, false)
	// 0700 exactly: svcendpoint refuses to publish a credential into anything wider.
	if err := os.MkdirAll(svcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(svcDir) })
	// The DEFAULT advertise host, exactly as a real daemon publishes it: a test that
	// published 127.0.0.1 would prove nothing about the substitution being labelled.
	ln, err := svcendpoint.Listen(filepath.Join(svcDir, "svc"+paths.ServiceEndpointExt), "")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{
		// Empty for every key, so inJail() is false — this suite runs inside a jail,
		// where the real YOLO_VERSION would send the section down the skip branch.
		Getenv:   func(string) string { return "" },
		LookPath: func(name string) (string, bool) { return "/bin/" + name, name == "podman" },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if len(argv) > 1 && argv[0] == "podman" && argv[1] == "ps" {
				return ExecResult{Ran: true, RC: 0, Stdout: cname + "\n"}
			}
			return ExecResult{Ran: true, RC: 1}
		},
	}
	fillDefaults(o)
	o.checkHostServiceLiveness(r)
	out := buf.String()

	if r.failed != 0 || !strings.Contains(out, "endpoint accepting") {
		t.Fatalf("the live listener did not pass: failed=%d out=%q", r.failed, out)
	}
	if !strings.Contains(out, "host-side") || !strings.Contains(out, "in-jail reachability") {
		t.Errorf("the green did not label itself host-side: out=%q", out)
	}
	// The caveat is the half that says where the answer this section cannot give
	// actually lives, and it must appear ONCE — a paragraph repeated under every
	// service is a paragraph nobody reads.
	if n := strings.Count(out, "the probes above are HOST-SIDE"); n != 1 {
		t.Errorf("host-side caveat appeared %d times, want exactly 1: out=%q", n, out)
	}
	if !strings.Contains(out, "in-jail probe") {
		t.Errorf("the caveat does not point at the probe that CAN answer: out=%q", out)
	}
	// The caveat is dim scaffolding, not a finding: it must not move the counts.
	if r.warned != 0 {
		t.Errorf("warned=%d — the caveat must not be graded: out=%q", r.warned, out)
	}
}

// TestHostServiceLivenessNoCaveatWithoutALoopbackTLSProbe is the other half of the
// caveat's contract, and the half a green suite was not holding: the footnote is
// gated on a loopback-TLS probe having actually run, and nothing measured the gate
// in the direction that turns it off.
//
// It matters because the caveat makes a claim about the probes above it — "they dial
// 127.0.0.1" — which is simply untrue of an AF_UNIX green: that socket is
// bind-mounted into the jail, not routed, so there is no forwarding hop to be wrong
// about and no in-jail question left unanswered. Printing it there is a paragraph
// that does not apply, under a section whose whole point is now saying exactly what
// it does and does not know.
func TestHostServiceLivenessNoCaveatWithoutALoopbackTLSProbe(t *testing.T) {
	fakeBundled := t.TempDir()
	oldBundled := loopholes.BundledLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return fakeBundled }
	t.Cleanup(func() { loopholes.BundledLoopholesDir = oldBundled })
	retiredLoopholeDir(t)

	modDir := filepath.Join(fakeBundled, "unixsvc")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// transport "none" WITH a host_daemon is the shape that reaches the AF_UNIX
	// branch: host_daemon is what puts it in the externals set, and any transport
	// other than loopback-tls is probed as a plain socket.
	if err := os.WriteFile(filepath.Join(modDir, "manifest.jsonc"), []byte(
		`{"name": "unixsvc", "description": "x", "transport": "none", `+
			`"default_enabled": true, "host_daemon": {"cmd": ["true"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	const cname = "yolo-check-unix-only-probe-test"
	svcDir := paths.HostServicesDir(cname, false)
	if err := os.MkdirAll(svcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(svcDir) })
	ln, err := net.Listen("unix", filepath.Join(svcDir, "unixsvc.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{
		Getenv:   func(string) string { return "" },
		LookPath: func(name string) (string, bool) { return "/bin/" + name, name == "podman" },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if len(argv) > 1 && argv[0] == "podman" && argv[1] == "ps" {
				return ExecResult{Ran: true, RC: 0, Stdout: cname + "\n"}
			}
			return ExecResult{Ran: true, RC: 1}
		},
	}
	fillDefaults(o)
	o.checkHostServiceLiveness(r)
	out := buf.String()

	// Guard the guard: without a green here the assertion below would pass for the
	// wrong reason — a section that probed nothing prints no caveat either.
	if r.failed != 0 || !strings.Contains(out, "socket accepting") {
		t.Fatalf("the unix socket did not pass: failed=%d out=%q", r.failed, out)
	}
	if strings.Contains(out, "the probes above are HOST-SIDE") {
		t.Errorf("a run with no loopback-TLS probe has no host-side caveat to make; "+
			"printing it here is how a reader learns to skip it where it matters: out=%q", out)
	}
}

// TestHostServiceLivenessInJailSaysWhy: run from inside a jail this section used to
// return SILENTLY, leaving its header standing over an empty block — which reads as
// "probed, nothing to report" in exactly the place where the honest answer is "not
// askable from here". Every sibling section announces why it stepped aside.
func TestHostServiceLivenessInJailSaysWhy(t *testing.T) {
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{Getenv: func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9-test"
		}
		return ""
	}}
	fillDefaults(o)
	o.checkHostServiceLiveness(r)
	out := buf.String()
	if r.failed != 0 || !strings.Contains(out, "Inside jail") ||
		!strings.Contains(out, "host-side") {
		t.Errorf("the in-jail skip is silent or unexplained: failed=%d out=%q", r.failed, out)
	}
}

// isolatedBundledDir points discovery at an EMPTY bundled dir and clears the retired
// one, so a test's own manifests are the only loopholes `yolo check` can find.
//
// Isolation is not politeness here: without it a developer's real bundled_loopholes/
// would have its doctor_cmds EXECUTED by every test in this file — the broker's among
// them — which is host execution from a unit test, and the counts would depend on the
// machine.
func isolatedBundledDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := loopholes.BundledLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return dir }
	t.Cleanup(func() { loopholes.BundledLoopholesDir = orig })
	retiredLoopholeDir(t)
	return dir
}

// writeLoopholeManifest writes <parent>/<name>/manifest.jsonc from the given body
// fields and returns the module dir. body is the manifest's interior, without braces.
func writeLoopholeManifest(t *testing.T, parent, name, body string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte("{"+body+"}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// selfCheckModule writes a module whose only host-side face is a doctor_cmd running
// argv, or no doctor_cmd at all when argv is empty.
//
// It stays inside the PACK-SHIPPED SUBSET (loophole-packaging.md §3.1: no jail_env, no
// ca_cert, no requires, no `publishes: "socket"`), because the same body is loaded as a
// bundled manifest in one test and as a pack module in another — and a pack module is
// read through loaderFor(SourcePack), which REFUSES anything outside the subset. One
// body that both loaders accept is what makes the two tests comparable.
func selfCheckModule(t *testing.T, parent, name string, argv []string) string {
	t.Helper()
	body := `"name":"` + name + `","description":"` + name + `","transport":"none",` +
		`"default_enabled":true`
	if len(argv) > 0 {
		body += `,"doctor_cmd":["` + strings.Join(argv, `","`) + `"]`
	}
	return writeLoopholeManifest(t, parent, name, body)
}

// touchAndSay is a doctor_cmd argv that records having run and prints one graded line.
func touchAndSay(sentinel, line string) []string {
	return []string{"/bin/sh", "-c", "touch " + sentinel + "; echo '" + line + "'"}
}

// recordPackModule records mod as this process's only pack-contributed loophole module,
// with the origin gate already decided, and clears the record afterwards.
//
// The record is deliberately process-wide — it IS the convergence point — so the
// cleanup is mandatory rather than tidy.
func recordPackModule(t *testing.T, mod string, approved bool) {
	t.Helper()
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, HostExecApproved: approved}})
	t.Cleanup(loopholes.ResetPackModules)
}

// runCheckLoopholes runs the health section against an empty workspace and returns the
// reporter and its rendered output.
func runCheckLoopholes(t *testing.T, workspace string) (*reporter, string) {
	t.Helper()
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	// Getenv empty for every key, so inJail() is false: this suite runs INSIDE a jail,
	// where the real YOLO_VERSION would send the section down the skip branch and every
	// assertion below would pass for the wrong reason.
	o := &Options{Workspace: workspace, Getenv: func(string) string { return "" }}
	fillDefaults(o)
	o.checkLoopholes(r)
	return r, buf.String()
}

// TestCheckLoopholesReportsAPackShippedSelfCheck is OQ-A12
// (docs/design/loophole-activation.md §4), and the reason it is not academic is that the
// activation sprint moves the ONLY two loopholes that declare a doctor_cmd — the broker
// and host-processes — out of bundled_loopholes/ and into packs. The health section used
// the package-level RunDoctorChecks, which refuses every SourcePack record by
// construction, so on the day that conversion lands the broker's cert freshness,
// liveness and self-check would have gone unreported under an all-green check.
//
// A BUNDLED loophole is present alongside the pack one, and asserted in the same run:
// the fix is "the pack source is seen too", never "the pack source replaced the ones
// that already worked".
func TestCheckLoopholesReportsAPackShippedSelfCheck(t *testing.T) {
	bundled := isolatedBundledDir(t)
	sentinels := t.TempDir()

	packRan := filepath.Join(sentinels, "pack-ran")
	packMod := selfCheckModule(t, t.TempDir(), "acme-proxy",
		touchAndSay(packRan, "NOTE: acme relay certificate expires in 3h"))
	recordPackModule(t, packMod, true)

	bundledRan := filepath.Join(sentinels, "bundled-ran")
	selfCheckModule(t, bundled, "corehole", touchAndSay(bundledRan, "OK: core wiring present"))

	r, out := runCheckLoopholes(t, t.TempDir())

	if _, err := os.Stat(packRan); err != nil {
		t.Fatalf("the pack-shipped doctor_cmd never ran:\n%s", out)
	}
	if !strings.Contains(out, "loophole acme-proxy: self-check ok") {
		t.Errorf("the pack-shipped self-check is not reported:\n%s", out)
	}
	// Its own graded output has to reach the screen too, or the loophole is "seen"
	// without anything it measured being readable — which is the exact shape of the
	// unreported cert freshness this change exists to prevent.
	if !strings.Contains(out, "acme relay certificate expires in 3h") || r.warned != 1 {
		t.Errorf("the pack self-check's NOTE line did not render as a warning "+
			"(warned=%d):\n%s", r.warned, out)
	}
	// And the non-pack source is untouched: same walk, same rendering, still executed.
	if _, err := os.Stat(bundledRan); err != nil {
		t.Fatalf("the BUNDLED doctor_cmd stopped running:\n%s", out)
	}
	if !strings.Contains(out, "loophole corehole: self-check ok") ||
		!strings.Contains(out, "core wiring present") {
		t.Errorf("the bundled self-check's reporting changed:\n%s", out)
	}
	if r.failed != 0 {
		t.Errorf("failed=%d — two passing self-checks:\n%s", r.failed, out)
	}
}

// A pack-shipped loophole that declares NO doctor_cmd must not have one invented for
// it. `audio-alsa` is that loophole today, and it is the reason the gap cost nothing
// until now — so the "no self-check declared" line is the state the fix must leave
// exactly as it found it, rather than a green that implies something was measured.
func TestCheckLoopholesDoesNotInventAPackSelfCheck(t *testing.T) {
	isolatedBundledDir(t)
	mod := selfCheckModule(t, t.TempDir(), "acme-quiet", nil)
	recordPackModule(t, mod, true)

	r, out := runCheckLoopholes(t, t.TempDir())

	if !strings.Contains(out, "loophole acme-quiet: no self-check declared") {
		t.Errorf("a pack loophole without a doctor_cmd is not reported as such:\n%s", out)
	}
	if strings.Contains(out, "self-check ok") || r.warned != 0 || r.failed != 0 {
		t.Errorf("a loophole declaring no self-check produced a health verdict "+
			"(warned=%d failed=%d):\n%s", r.warned, r.failed, out)
	}
}

// The gate still refuses, and still SAYS SO. Reading pack loopholes through the Set is
// not a way past the origin gate: an unapproved pack's doctor_cmd is host execution
// from a command AGENTS.md treats as read-only preflight, and the refusal lives in the
// callee where a slice cannot forget it.
//
// It has to be VISIBLE, which is why this asserts the wording and not merely the
// absence of the sentinel: silence would render as "no self-check declared", telling
// the reader the loophole measures nothing when in fact its measurement was withheld —
// and the fix (`yolo pack install` records the approval) is not discoverable from an
// absence.
func TestCheckLoopholesWithholdsAnUnapprovedPackSelfCheck(t *testing.T) {
	isolatedBundledDir(t)
	ran := filepath.Join(t.TempDir(), "ran")
	mod := selfCheckModule(t, t.TempDir(), "acme-evil", touchAndSay(ran, "OK: harmless"))
	recordPackModule(t, mod, false)

	r, out := runCheckLoopholes(t, t.TempDir())

	if _, err := os.Stat(ran); err == nil {
		t.Fatal("THE DOCTOR_CMD RAN. `yolo check` is read-only preflight; running an " +
			"unapproved pack's host code from it is the fork loophole-packaging.md §5.1 " +
			"refuses to leave open")
	}
	if r.warned != 1 || !strings.Contains(out, "self-check could not run") ||
		!strings.Contains(out, "not approved") {
		t.Errorf("the withheld self-check is not reported with its reason (warned=%d):\n%s",
			r.warned, out)
	}
	if strings.Contains(out, "no self-check declared") {
		t.Errorf("a WITHHELD self-check was rendered as a loophole that declares none:\n%s", out)
	}
}

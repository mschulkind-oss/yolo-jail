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
	writeManifest("wsoff", `{"name": "wsoff", "transport": "none", "enabled": true}`)
	writeManifest("selfoff", `{"name": "selfoff", "transport": "none", "enabled": false}`)

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
			`"host_daemon": {"cmd": ["true"]}}`), 0o644); err != nil {
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

package integration

// sessionhome_test.go is the integration tier of docs/design/per-session-profiles.md: one jail,
// several sessions, each reading its OWN pi config. A first session holds the jail open on the
// configured profile; an attach with another `-p`, and a bare attach running no pi at all, each get
// a session home of their own (the pi pack's `session_homes` declaration, PI_CODING_AGENT_DIR
// exported by the session pass), and neither changes what the first session reads. No agent CLI
// runs: each session prints what its pi WOULD read — the exported directory and the provider its
// settings.json names — which is exactly the file stock pi and pi-subagents read through
// getAgentDir() (§2.2). The unit pins are internal/entrypoint's session-home tests and
// internal/cli/run's snapshot tests; only a real attach proves the host's snapshot and the jail's
// pass meet.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// sessionHomeProbeTemplate prints, for the session it runs in, the directory pi would read its config
// from, the provider that directory's settings.json names, the shared copy's provider, the
// settings file's digest, and whether auth.json is linked to the shared login.
const sessionHomeProbeTemplate = `printf 'DIR=%s\n' "${AGENT_DIR_VAR-unset}"; ` +
	`printf 'PROV=%s\n' "$(jq -r .defaultProvider "${AGENT_DIR_VAR:-/nonexistent}/settings.json" 2>/dev/null)"; ` +
	`printf 'SHARED=%s\n' "$(jq -r .defaultProvider ~/.pi/agent/settings.json 2>/dev/null)"; ` +
	`printf 'SUM=%s\n' "$(sha256sum "${AGENT_DIR_VAR:-/nonexistent}/settings.json" 2>/dev/null | cut -c1-16)"; ` +
	`if [ -L "${AGENT_DIR_VAR:-/nonexistent}/auth.json" ]; then echo AUTH=linked; else echo AUTH=missing; fi; ` +
	`if [ -f "${AGENT_DIR_VAR:-/nonexistent}/extensions/yolo-provider-policy.js" ]; then echo EXT=present; else echo EXT=missing; fi`

// sessionHomeProbe is the probe for the variable the shipped pi pack's session-home declaration
// names, read from the pack rather than spelled here.
func sessionHomeProbe(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "packs", "pi", packdecl.ManifestTOMLName))
	if err != nil {
		t.Fatal(err)
	}
	m, problems := packdecl.DecodeTOML(data)
	if len(problems) > 0 || len(m.SessionHomes()) != 1 {
		t.Fatalf("the shipped pi pack must declare one session home: %v", problems)
	}
	return strings.ReplaceAll(sessionHomeProbeTemplate, "AGENT_DIR_VAR", m.SessionHomes()[0].Env)
}

// Each field is matched as a whole LINE: the boot echoes the command it runs, probe text and all,
// on a line of its own that starts elsewhere.
var sessionHomeField = regexp.MustCompile(`(?m)^(DIR|PROV|SHARED|SUM|AUTH|EXT)=(.*)$`)

// The first session's two markers, as LINES: the boot echoes the command it runs, so the bare
// literal appears before the session has printed anything (firstHasAnswer's lesson).
var (
	aReadyLine = regexp.MustCompile(`(?m)^A-READY$`)
	aAfterLine = regexp.MustCompile(`(?m)^A-AFTER$`)
)

// sessionHomeReport parses one probe's output.
func sessionHomeReport(t *testing.T, out string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, m := range sessionHomeField.FindAllStringSubmatch(out, -1) {
		got[m[1]] = m[2]
	}
	return got
}

func TestEachSessionReadsItsOwnPiProfile(t *testing.T) {
	requireJail(t)
	probe := sessionHomeProbe(t)
	// Both providers' keys ride [env.secrets], the channel the credential gate delivers into pi's
	// own env file (as in TestPiRunsOnEveryProviderOfItsSet); the shell's copies are blanked.
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	dir := writeProject(t, `{}`)
	writeUserTOML(t, "packs = [\"pi\", \"zai\", \"openrouter\"]\n\n[active_provider]\npi = \"zai\"\n\n[env.secrets]\n"+
		"set = { ZAI_API_KEY = \"integration-probe-not-a-real-key\", OPENROUTER_API_KEY = \"integration-probe-not-a-real-key\" }\n")

	// Session A: the fresh launch's first session, on the configured profile (OQ-SP3: no -p is the
	// configured profile). It reports, then holds the jail open until released, then reports again
	// so the test can see A's file after B and C entered.
	const release = "release-session-home"
	first := startYoloBackground(t, "first", dir,
		probe+`; echo A-READY; `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done; `+
			`echo A-AFTER; `+probe)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, release), []byte("go\n"), 0o644) })
	deadline := time.Now().Add(jailTimeout())
	for !aReadyLine.MatchString(first.combined()) {
		select {
		case err := <-first.done:
			t.Fatalf("first launch exited (%v) before its session reported:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("first session never reported within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	awaitLaunchLockReleased(t, dir, first)
	before := first.combined()
	a := sessionHomeReport(t, before[:aReadyLine.FindStringIndex(before)[0]])

	// Session B: an attach on another profile.
	rb := runCommand(t, dir, append(jailRunArgs(), "-p", "pi=openrouter", "--", "bash", "-lc", probe))
	if rb.rc != 0 {
		t.Fatalf("the -p attach failed: rc %d\n%s", rb.rc, rb.combined())
	}
	if !strings.Contains(rb.combined(), "Attaching to existing jail") {
		t.Fatalf("the second entry must attach to the running jail:\n%s", rb.combined())
	}
	b := sessionHomeReport(t, rb.stdout)

	// Session C: a bare attach running no pi (P5: core cannot tell what an attach runs).
	rc := runCommand(t, dir, append(jailRunArgs(), "--", "bash", "-lc", probe))
	if rc.rc != 0 {
		t.Fatalf("the bare attach failed: rc %d\n%s", rc.rc, rc.combined())
	}
	c := sessionHomeReport(t, rc.stdout)

	if err := os.WriteFile(filepath.Join(dir, release), []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.exited:
	case <-time.After(jailTimeout()):
		t.Fatalf("first session did not end after release:\n%s", first.combined())
	}
	all := first.combined()
	loc := aAfterLine.FindStringIndex(all)
	if loc == nil {
		t.Fatalf("first session never reported after release:\n%s", all)
	}
	after := sessionHomeReport(t, all[loc[0]:])

	for name, r := range map[string]map[string]string{"A": a, "B": b, "C": c} {
		if r["DIR"] == "" || r["DIR"] == "unset" || !strings.Contains(r["DIR"], "/.pi/.yolo-sessions/") {
			t.Errorf("session %s: the session-home variable = %q, want a session home under ~/.pi/.yolo-sessions/", name, r["DIR"])
		}
		if r["AUTH"] != "linked" {
			t.Errorf("session %s: auth.json in its session home must link the shared login (P3)", name)
		}
		if r["EXT"] != "present" {
			t.Errorf("session %s: yolo's pi extensions must be reachable from its session home", name)
		}
	}
	if a["DIR"] == b["DIR"] || a["DIR"] == c["DIR"] || b["DIR"] == c["DIR"] {
		t.Errorf("each session must get its own home: A %q, B %q, C %q", a["DIR"], b["DIR"], c["DIR"])
	}
	if a["PROV"] != "zai" {
		t.Errorf("session A (configured profile) reads defaultProvider %q, want zai", a["PROV"])
	}
	if b["PROV"] != "openrouter" {
		t.Errorf("session B (-p pi=openrouter) reads defaultProvider %q, want openrouter", b["PROV"])
	}
	if c["PROV"] != "zai" {
		t.Errorf("session C (bare attach) reads defaultProvider %q, want the configured zai", c["PROV"])
	}
	// The deadlock's end-to-end form: B's and C's entries leave A's file as it was.
	if after["DIR"] != a["DIR"] || after["SUM"] != a["SUM"] || after["PROV"] != "zai" {
		t.Errorf("session A's config changed under it after B and C entered: before %v, after %v", a, after)
	}
	// SP-D2: no session pass rewrote the shared copy; it is still the launch's own render.
	if b["SHARED"] != "zai" || after["SHARED"] != "zai" {
		t.Errorf("the shared ~/.pi/agent/settings.json moved (B saw %q, A after saw %q); only the jail's boot writes it",
			b["SHARED"], after["SHARED"])
	}
}

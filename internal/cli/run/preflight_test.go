package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// preflight_test.go pins OQ-CAP2's refusal (docs/design/agent-auth-modes.md §6.2): a
// config that DECLARES it needs a capability, and declares nothing that provides it,
// stops the launch instead of starting a jail whose agent discovers the hole at its
// first request.
//
// ⚠ THE CALL SITE IS WHAT THESE ASSERT, not the predicate. AGENTS.md's "a test that pins
// the CALLEE while the CALL SITE is unpinned is not a test" is the exact shape this
// feature would otherwise take: refuseUnmetCapabilities is one function, and a direct
// unit test of it passes with its call deleted from loadAndValidateConfig — which is the
// whole feature. So every case below calls Run() and discriminates on WHICH refusal came
// back: the workspace has no resolvable repo root, so a launch that gets past the
// capability gate refuses a few lines later with that message instead. Delete the call
// site and the refusal cases fail on the substitution; keep it and the control cases
// fail if the gate refuses a launch it must let through.

// capabilityGateOptions builds an Options whose seams reach the config gate
// deterministically: storage and config load trivially, the runtime is named explicitly
// so no real daemon is consulted, and RepoRoot FAILS — which is what makes "did the gate
// fire?" observable as a difference between two refusals.
//
// Self-contained rather than reusing notchgate_test.go's sibling, for the reason that
// file records about its own: the discriminator these tests rest on is that helper's
// subject, and sharing a fixture would let a change there quietly rewrite what this file
// measures.
func capabilityGateOptions(t *testing.T, ws string, env map[string]string,
	stdout, stderr *bytes.Buffer) *Options {
	t.Helper()
	o := &Options{
		Workspace: ws,
		Network:   "bridge",
		IsLinux:   true,
		Stdout:    stdout,
		Stderr:    stderr,
	}
	fillDefaults(o)
	// fillDefaults re-installs the real seams; re-apply the deterministic stubs.
	o.Stdout = stdout
	o.Stderr = stderr
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.Now = func() time.Time { return time.Unix(0, 0) }
	o.Getpid = func() int { return 1 }
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) >= 2 && argv[0] == "podman" && argv[1] == "info" {
			return ExecResult{Ran: true, RC: 0, Stdout: "host: {}"}
		}
		return ExecResult{Ran: false}
	}
	o.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return "podman"
		}
		return env[k]
	}
	// No flake anywhere: a launch that survives the capability gate refuses on THIS
	// instead, which is the discriminator every case below reads.
	o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false }
	return o
}

// capabilityWorkspace writes a workspace whose yolo-jail.jsonc is `body`, with an empty
// user scope beside it so the merged config is exactly what the test wrote.
func capabilityWorkspace(t *testing.T, body string) string {
	t.Helper()
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

// mustDecodeConfig parses a config literal into the merged-config shape the gate reads.
func mustDecodeConfig(t *testing.T, s string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("not an object: %T", v)
	}
	return m
}

const gotPastTheGate = "Cannot find yolo-jail repo root"

// TestLaunchRefusesACapabilityNothingDeclares is the refusal itself: `web_search` is
// required, no provider claims it and no MCP server provides it, so the launch stops and
// NAMES the capability.
//
// Until 2026-09-17 this config launched happily and exported the requirement to the jail
// as YOLO_REQUIRED_CAPABILITIES, where nothing read it (setup-support-gaps.md G9).
func TestLaunchRefusesACapabilityNothingDeclares(t *testing.T) {
	ws := capabilityWorkspace(t, `{"required_capabilities": ["web_search"]}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	rc := Run(*o)

	if rc != 1 {
		t.Fatalf("Run() = %d, want 1 (an unmet required capability refuses)\nstdout:\n%s\nstderr:\n%s",
			rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "web_search") {
		t.Errorf("the refusal does not NAME the missing capability, which is the one thing "+
			"§6.2 requires of it:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), AllowUnmetCapabilitiesEnv) {
		t.Errorf("the refusal does not name its escape hatch, so a user whose satisfier "+
			"yolo cannot see has nowhere to go:\n%s", stderr.String())
	}
	// THE DISCRIMINATOR. This workspace has no repo root, so a launch that reached the
	// container arm would refuse with that instead. Seeing it here means the capability
	// gate did not run — which is what deleting its call site looks like.
	if strings.Contains(stderr.String(), gotPastTheGate) {
		t.Errorf("the launch got PAST the capability gate and refused for another reason — "+
			"the gate is not wired into loadAndValidateConfig:\n%s", stderr.String())
	}
}

// TestAnMCPServerSatisfiesARequiredCapability is the first CONTROL, and without the
// controls the refusal above is satisfied by a gate that refuses every launch. An
// `mcp_servers` entry declaring `provides` is §6.2's second satisfier.
func TestAnMCPServerSatisfiesARequiredCapability(t *testing.T) {
	ws := capabilityWorkspace(t, `{
	  "required_capabilities": ["web_search"],
	  "mcp_servers": {"tavily": {"command": "npx", "provides": "web_search"}}
	}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	Run(*o)

	if !strings.Contains(stderr.String(), gotPastTheGate) {
		t.Errorf("a capability an mcp_servers entry PROVIDES was refused anyway — the gate "+
			"is reading only half its census:\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
}

// TestAProviderCapabilitySatisfiesARequiredCapability is the other half of the census:
// `providers.<name>.capabilities` is where "the agent has it natively" is declared.
func TestAProviderCapabilitySatisfiesARequiredCapability(t *testing.T) {
	ws := capabilityWorkspace(t, `{
	  "required_capabilities": ["web_search"],
	  "providers": {"anthropic": {"capabilities": ["web_search"]}}
	}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	Run(*o)

	if !strings.Contains(stderr.String(), gotPastTheGate) {
		t.Errorf("a capability a provider DECLARES was refused anyway:\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
}

// TestTheCapabilityBaselineNeedsNoDeclaration guards the documented spelling: `yolo
// config-ref`'s own example for this key is ["code_editing"], and §6.2 calls the two
// baseline names the requirement every agent meets. A gate that refused them would refuse
// the config reference's example.
func TestTheCapabilityBaselineNeedsNoDeclaration(t *testing.T) {
	ws := capabilityWorkspace(t, `{"required_capabilities": ["code_editing", "command_execution"]}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	Run(*o)

	if !strings.Contains(stderr.String(), gotPastTheGate) {
		t.Errorf("the baseline capabilities were refused — nothing declares them because "+
			"every agent meets them:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

// TestANullRemovedMCPServerSatisfiesNothing: `"tavily": null` is the spelling that REMOVES
// a server, so the entry must not go on satisfying what the running one would have. Read
// off the merged VALUE rather than the key set, which is the only way to tell the two
// apart.
func TestANullRemovedMCPServerSatisfiesNothing(t *testing.T) {
	ws := capabilityWorkspace(t, `{
	  "required_capabilities": ["web_search"],
	  "mcp_servers": {"tavily": null}
	}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	rc := Run(*o)

	if rc != 1 || strings.Contains(stderr.String(), gotPastTheGate) {
		t.Errorf("a null-removed mcp_servers entry satisfied a capability — the jail deletes "+
			"that server, so nothing provides it (rc=%d):\nstdout:\n%s\nstderr:\n%s",
			rc, stdout.String(), stderr.String())
	}
}

// TestTheCapabilityHatchContinuesLoudly pins the hatch's SHAPE, which is the half that
// keeps it from being a silent off switch: set, the launch continues AND says what it is
// suppressing (providerpreflight.go's rule — nothing was repaired).
func TestTheCapabilityHatchContinuesLoudly(t *testing.T) {
	ws := capabilityWorkspace(t, `{"required_capabilities": ["web_search"]}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws,
		map[string]string{AllowUnmetCapabilitiesEnv: "1"}, &stdout, &stderr)

	Run(*o)

	if !strings.Contains(stderr.String(), gotPastTheGate) {
		t.Errorf("%s did not let the launch past the capability gate:\nstdout:\n%s\nstderr:\n%s",
			AllowUnmetCapabilitiesEnv, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), AllowUnmetCapabilitiesEnv) ||
		!strings.Contains(stderr.String(), "web_search") {
		t.Errorf("the hatch suppressed the refusal QUIETLY — the notice must name the hatch "+
			"and the capability it is continuing without:\n%s", stderr.String())
	}
}

// TestUnmetCapabilitiesReportsInDeclarationOrderWithoutRepeats is the one direct test
// here, and it is a SUPPLEMENT to the Run()-level cases above rather than a substitute:
// it pins what the refusal SAYS (order and de-duplication), which the call-site tests
// deliberately do not assert.
func TestUnmetCapabilitiesReportsInDeclarationOrderWithoutRepeats(t *testing.T) {
	cfg := mustDecodeConfig(t, `{
	  "required_capabilities": ["image_generation", "web_search", "image_generation", "code_editing"],
	  "mcp_servers": {"tavily": {"command": "npx", "provides": "web_search"}}
	}`)

	got := unmetCapabilities(cfg)

	want := []string{"image_generation"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("unmetCapabilities() = %v, want %v (web_search is provided, code_editing is "+
			"baseline, and a name declared twice is one gap)", got, want)
	}
}

package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	// podman answers the readiness gate at once (podmanready.go).
	answeringPodman(o, minimalPodmanInfo)
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

// TestTheRefusalNamesEachGapOnceInDeclarationOrder pins what the refusal SAYS, which the
// cases above deliberately do not assert: web_search is provided and code_editing is
// baseline, so only image_generation is named, once although it is declared twice. The census
// itself is config.UnmetCapabilities, whose own tests pin its rows.
func TestTheRefusalNamesEachGapOnceInDeclarationOrder(t *testing.T) {
	ws := capabilityWorkspace(t, `{
	  "required_capabilities": ["image_generation", "web_search", "image_generation", "code_editing"],
	  "mcp_servers": {"tavily": {"command": "npx", "provides": "web_search"}}
	}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	Run(*o)

	if !strings.Contains(stderr.String(), "declares 'image_generation', and nothing") {
		t.Errorf("the refusal must name image_generation alone, once (web_search is provided, "+
			"code_editing is baseline, and a name declared twice is one gap):\n%s", stderr.String())
	}
}

// --- A SELECTED PACK'S OWN DECLARATIONS (agent-auth-modes.md §6.1 clause 1) ---
//
// The cases above pin the census of the user's config. These pin its third surface: a pack
// declares what its agent's built-in login does (`capabilities` on its `program`) and what the
// providers it ships do (`capabilities` on its `provider`), and a launch that SELECTS that pack
// has those capabilities. Until 2026-09-30 the gate read the merged user config alone, so
// requiring `web_search` with claude selected refused the launch although claude searches
// natively, and the only way through was the hatch.
//
// Each case selects packs through the user config, exactly as a launch does, and drives Run(),
// so deleting the pack half of the census, or resolving a different pack set than the launch
// selects, fails a case here rather than only a helper's test.

// capabilityPackHome writes the user config into capabilityWorkspace's HOME: `packs` is
// user-scope only, so a selection can only be written there.
func capabilityPackHome(t *testing.T, userBody string) {
	t.Helper()
	writeUserConfig(t, os.Getenv("HOME"), userBody)
}

// refusedForTheCapability reports whether stderr carries the capability gate's own refusal: a
// control case must refuse for THIS reason, not because something else in its config was wrong.
func refusedForTheCapability(stderr string) bool {
	return strings.Contains(stderr, "Refusing to launch: config.required_capabilities declares")
}

// TestASelectedPacksAgentCapabilitySatisfiesARequiredCapability is the roadmap's case: claude is
// selected, its pack declares web_search for claude's built-in login, and the launch must not be
// refused for wanting search.
func TestASelectedPacksAgentCapabilitySatisfiesARequiredCapability(t *testing.T) {
	ws := capabilityWorkspace(t, `{"required_capabilities": ["web_search"]}`)
	capabilityPackHome(t, `{"packs": ["claude"]}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	Run(*o)

	if !strings.Contains(stderr.String(), gotPastTheGate) {
		t.Errorf("claude is selected and its pack declares web_search, yet the launch did not "+
			"get past the capability gate:\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
}

// TestAPacksProgramCapabilitySatisfiesAlone isolates the `program` half: agy needs no other pack,
// so nothing but its own built-in login's declaration can satisfy the name.
func TestAPacksProgramCapabilitySatisfiesAlone(t *testing.T) {
	ws := capabilityWorkspace(t, `{"required_capabilities": ["web_search"]}`)
	capabilityPackHome(t, `{"packs": ["agy"]}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	Run(*o)

	if !strings.Contains(stderr.String(), gotPastTheGate) {
		t.Errorf("agy's pack declares web_search on its program, and nothing else in this "+
			"launch could satisfy it, so the gate ignores a program's declaration:\n"+
			"stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

// TestAPacksProviderCapabilitySatisfiesAlone isolates the `provider` half: zai installs no agent
// and needs no pack, and the provider it ships declares web_search. It reaches the launch through
// the composed providers table, the one every derive reads.
func TestAPacksProviderCapabilitySatisfiesAlone(t *testing.T) {
	ws := capabilityWorkspace(t, `{"required_capabilities": ["web_search"]}`)
	capabilityPackHome(t, `{"packs": ["zai"]}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	Run(*o)

	if !strings.Contains(stderr.String(), gotPastTheGate) {
		t.Errorf("zai's pack ships a provider declaring web_search, yet the launch did not get "+
			"past the capability gate:\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
}

// TestAUserOverrideOfAPacksProviderCapabilitiesWins is the control that makes the provider half
// the launch's COMPOSITION rather than a walk over pack declarations: the user's
// `providers.zai.capabilities` replaces the pack's list, as it does in the table the launch
// delivers, so an empty list there declares nothing and the launch refuses.
func TestAUserOverrideOfAPacksProviderCapabilitiesWins(t *testing.T) {
	ws := capabilityWorkspace(t, `{"required_capabilities": ["web_search"]}`)
	capabilityPackHome(t, `{"packs": ["zai"], "providers": {"zai": {"capabilities": []}}}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	rc := Run(*o)

	if rc != 1 || !refusedForTheCapability(stderr.String()) {
		t.Errorf("the user's providers.zai.capabilities: [] overrides the pack's list in the "+
			"composed table, so nothing provides web_search and the gate must refuse "+
			"(rc=%d):\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
}

// TestACensusThatCouldNotReadAPackDoesNotRefuse: the one selected pack cannot be read, so it may
// be the satisfier, and pack staging refuses this launch itself, naming the pack. The gate must
// neither refuse first (the second fault named first) nor stay silent about what it skipped.
func TestACensusThatCouldNotReadAPackDoesNotRefuse(t *testing.T) {
	ws := capabilityWorkspace(t, `{"required_capabilities": ["web_search"]}`)
	capabilityPackHome(t, `{"packs": ["`+filepath.Join(t.TempDir(), "no-such-pack")+`"]}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	Run(*o)

	if refusedForTheCapability(stderr.String()) {
		t.Errorf("a selected pack could not be read, so the census proves nothing, and the "+
			"gate refused anyway:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot tell whether anything satisfies required "+
		"capability 'web_search'") {
		t.Errorf("the gate skipped its census silently; it must say what it could not check:\n"+
			"stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

// TestAnUnselectedPacksCapabilitySatisfiesNothing is the selection control: copilot is selected,
// declares no capability for its own login and needs no pack that does, so a census reading
// every pack yolo SHIPS (agy, claude, zai) instead of the ones this launch selects would let this
// launch through.
func TestAnUnselectedPacksCapabilitySatisfiesNothing(t *testing.T) {
	ws := capabilityWorkspace(t, `{"required_capabilities": ["web_search"]}`)
	capabilityPackHome(t, `{"packs": ["copilot"]}`)
	var stdout, stderr bytes.Buffer
	o := capabilityGateOptions(t, ws, nil, &stdout, &stderr)

	rc := Run(*o)

	if rc != 1 || !refusedForTheCapability(stderr.String()) {
		t.Errorf("nothing selected declares web_search, so the gate must refuse "+
			"(rc=%d):\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
}

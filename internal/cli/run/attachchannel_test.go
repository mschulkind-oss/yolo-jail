package run

// attachchannel_test.go pins the attach arm's channel delivery — the §4.3 half the
// attach branch never had until per-entry delivery existed. Three pins:
//
//   - the WRITE that delivers (the same writeUserEnvFile the fresh path performs,
//     against the live-mounted file the exec'd boot hydrates);
//   - the credential PRE-FLIGHT, moved to this arm with the delivery;
//   - the REFUSAL for a pre-change jail, whose frozen environment this write cannot
//     override — a typed -p against such a jail must be loud, not silently inert.
//
// The exec itself is not driven here (runWithProxy spawns a real runtime; the
// integration suite owns the end-to-end), which is why attachExisting's call site
// is AST-pinned beside the behavioral callee tests: deleting the call passes the
// callee tests and fails the pin, the combination the repo's call-site rule asks for.

import (
	"bytes"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// attachFixture builds one attach attempt against a faked running jail: inspect
// answers from frozenEnv (the container's Config.Env, one entry per line), and the
// channel is composed from the real packs/config/userEnv triple exactly as Run
// composes it above the backend dispatch.
func attachFixture(t *testing.T, frozenEnv string, packs []*packload.Pack,
	userEnv *jsonx.OrderedMap, tune func(*Options, *jsonx.OrderedMap)) (*Options, *jsonx.OrderedMap, *packChannel, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	ws := t.TempDir()
	o := goldenOptions(ws, home)
	var stdout, stderr bytes.Buffer
	o.Stdout = &stdout
	o.Stderr = &stderr
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "inspect" {
			return ExecResult{Ran: true, RC: 0, Stdout: frozenEnv}
		}
		return ExecResult{Ran: false}
	}
	cfg := bareConfig()
	if tune != nil {
		tune(o, cfg)
	}
	channel := channelFor(t, o, cfg, packs, userEnv)
	return o, cfg, channel, &stderr
}

// configSelects puts the CONFIG-side spelling of a selection on the fixture's cfg —
// the persistent use_profiles table, as opposed to the -p flag fields on Options,
// which the pre-change check must treat differently (typed vs config).
func configSelects(cfg *jsonx.OrderedMap, cli, profile string) {
	profiles := jsonx.NewOrderedMap()
	profiles.Set(cli, profile)
	cfg.Set("use_profiles", profiles)
}

// TestAttachDeliversTheChannelFile is the positive pin on the WRITE: a post-change
// jail (no YOLO_PROVIDERS in its frozen env) with the zai profile selected gets the
// channel section into its live-mounted yolo-user-env.sh, and the disclosure line
// prints beside it — the same sentence the fresh launch prints, because this entry
// delivers the same thing.
func TestAttachDeliversTheChannelFile(t *testing.T) {
	packs := zaiSelected(t)
	o, cfg, channel, stderr := attachFixture(t, "YOLO_VERSION=9.9.9-test\n",
		packs, hydratedKey(), func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })

	rc := o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, []string{"YOLO_VERSION=9.9.9-test"})
	if rc != 0 {
		t.Fatalf("delivery refused a healthy attach: rc=%d\n%s", rc, stderr.String())
	}
	file := filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the attach never wrote the channel file: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		"export YOLO_USE_PROFILES=",
		"export ANTHROPIC_BASE_URL='https://api.z.ai/api/anthropic'",
		"export ANTHROPIC_AUTH_TOKEN='tok-9'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("channel file missing %s:\n%s", want, s)
		}
	}
	if out := stderr.String(); !strings.Contains(out, "Profile zai: declared: zai") {
		t.Errorf("the attach must print where the selection landed:\n%s", out)
	}
}

// preChangeEnv is a pre-change jail's frozen environment: the wire tables on the
// container (post-change argv carries none), with the launch-time selection in
// YOLO_USE_PROFILES — the baseline a re-entering entry is compared against.
const preChangeEnv = "YOLO_VERSION=0.8.0\n" +
	"YOLO_PROVIDERS={\"zai\": {}}\n" +
	"YOLO_USE_PROFILES={\"claude\": \"zai\"}\n"

// preChangePacks declares BOTH selections the pre-change tests move between (the
// frozen zai and the differing cerebras) — declaration is mandatory (OQ-CS6), so a
// name the fixture's pack set does not declare would refuse at composition, one
// layer before the behaviour under test.
func preChangePacks(t *testing.T) []*packload.Pack {
	return []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "zai"), officialPack(t, "cerebras"),
	}
}

// TestAttachRefusesATypedProfileOnAPreChangeJail: a jail launched before the
// channel moved onto the file cannot take a DIFFERENT typed selection — its old
// hydrate lets the frozen environment beat the file — so a typed -p refuses rather
// than run silently inert. The remedy must name a RESTART, and must not recommend
// 'yolo --new': --new force-removes the RUNNING container and its sessions, which
// was the first cut's advice and was measured doing exactly that on a live jail.
func TestAttachRefusesATypedProfileOnAPreChangeJail(t *testing.T) {
	packs := preChangePacks(t)
	o, cfg, channel, stderr := attachFixture(t, preChangeEnv,
		packs, hydratedKey(), func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "cerebras" })

	rc := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false)
	if rc != 1 {
		t.Fatalf("a typed profile a pre-change jail cannot take must refuse, rc=%d", rc)
	}
	out := stderr.String()
	if !strings.Contains(out, "Refusing to attach") {
		t.Errorf("the refusal must say so:\n%s", out)
	}
	if !strings.Contains(out, "podman stop yolo-ws-abcd1234") {
		t.Errorf("the remedy must name the jail's stop command:\n%s", out)
	}
	if strings.Contains(out, "'yolo --new' to get") || strings.Contains(out, "Relaunch the jail ('yolo --new')") {
		t.Errorf("the remedy must not recommend --new, which force-kills the running jail:\n%s", out)
	}
	// And it refuses BEFORE delivering: nothing was written into the workspace.
	if _, err := os.Stat(filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")); !os.IsNotExist(err) {
		t.Errorf("a refused attach must not write the channel file")
	}
}

// TestAttachToAPreChangeJailWithMatchingSelectionIsSilent: the plain re-entry. A
// pre-change jail whose frozen selection matches what this entry selects (or where
// this entry selects nothing) must behave exactly as attaches did before per-entry
// delivery existed — no refusal, no warning, no delivery. The first cut of the
// pre-change check refused every attach whose table was merely NON-EMPTY, which
// broke the daily 'yolo -- <cmd>' of any config carrying a persistent
// use_profiles (measured on a live jail, 2026-09-05).
func TestAttachToAPreChangeJailWithMatchingSelectionIsSilent(t *testing.T) {
	packs := preChangePacks(t)
	for _, tc := range []struct {
		name string
		fenv string
		tune func(*Options, *jsonx.OrderedMap)
	}{
		{"config selection matches the frozen one", preChangeEnv, func(o *Options, cfg *jsonx.OrderedMap) {
			configSelects(cfg, "claude", "zai")
		}},
		{"typed selection matches the frozen one", preChangeEnv, func(o *Options, _ *jsonx.OrderedMap) {
			o.UseProfiles = map[string]string{"claude": "zai"}
		}},
		{"no selection at all", preChangeEnv, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, tc.fenv, packs, hydratedKey(), tc.tune)
			rc := o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg,
				stagedPacks{root: "/ctx/packs", packs: packs}, channel,
				strings.Split(preChangeEnv, "\n"))
			if rc != 0 {
				t.Fatalf("a plain re-entry into a pre-change jail must not refuse: rc=%d\n%s",
					rc, stderr.String())
			}
			if out := stderr.String(); strings.Contains(out, "Refusing") || strings.Contains(out, "predates per-entry") {
				t.Errorf("a plain re-entry must be silent, not warned:\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")); !os.IsNotExist(err) {
				t.Errorf("a plain re-entry into an old jail must not write the channel file")
			}
		})
	}
}

// TestAttachToAPreChangeJailWarnsOnConfigDrift: the config's persistent selection
// moved after the jail launched, and nothing was typed. The jail keeps running its
// launch-time providers (delivery cannot reach it), the attach proceeds — refusing
// here would hold a workspace's re-entry hostage to a one-time upgrade — and a
// warning says what is running instead.
func TestAttachToAPreChangeJailWarnsOnConfigDrift(t *testing.T) {
	packs := preChangePacks(t)
	o, cfg, channel, stderr := attachFixture(t, preChangeEnv, packs, hydratedKey(),
		func(o *Options, cfg *jsonx.OrderedMap) { configSelects(cfg, "claude", "cerebras") })

	rc := o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel,
		strings.Split(preChangeEnv, "\n"))
	if rc != 0 {
		t.Fatalf("untyped config drift must warn, not refuse: rc=%d\n%s", rc, stderr.String())
	}
	if out := stderr.String(); !strings.Contains(out, "predates per-entry profiles") {
		t.Errorf("the drift warning must say what the jail is running:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")); !os.IsNotExist(err) {
		t.Errorf("an undeliverable attach must not write the channel file")
	}
}

// TestAttachRunsTheCredentialPreflight: the attach arm delivers environment, so an
// unhydratable key refuses HERE too — a session that would start with
// ANTHROPIC_BASE_URL and no token is §6.1's mysterious first-API-call failure. The
// stale-jail refusal is out of the way (post-change frozen env), so the refusal
// names the variable, not the jail.
func TestAttachRunsTheCredentialPreflight(t *testing.T) {
	packs := zaiSelected(t)
	// No hydrated key and no ZAI_API_KEY in the invoking environment.
	o, cfg, channel, stderr := attachFixture(t, "YOLO_VERSION=9.9.9-test\n",
		packs, emptyEnv(), func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })

	rc := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false)
	if rc != 1 {
		t.Fatalf("an attach that cannot hydrate the selected provider's key must refuse, rc=%d", rc)
	}
	if out := stderr.String(); !strings.Contains(out, "ZAI_API_KEY") {
		t.Errorf("the refusal must name the variable:\n%s", out)
	}
}

// TestAttachExistingCallsTheChannelDelivery is the call-site pin the two behavioral
// tests above cannot be: deliverChannelOnAttach is a method this file can test
// directly, and nothing about those tests would notice if attachExisting stopped
// calling it — the exact shape AGENTS.md names. Reading the source is the repo's
// existing answer (TestFreshLaunchPrintsTheProfileLineBesideTheHostAccessLine,
// which this mirrors).
func TestAttachExistingCallsTheChannelDelivery(t *testing.T) {
	fn := methodDecl(t, "run.go", "attachExisting")
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "deliverChannelOnAttach" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatalf("attachExisting no longer calls deliverChannelOnAttach. The delivery " +
			"would then exist unprinted and uncalled — 'yolo -p <name> -- claude' against a " +
			"running jail back to silently dropping the selection. If the delivery moved, " +
			"move this check with it rather than deleting it.")
	}
}

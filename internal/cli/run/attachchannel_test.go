package run

// attachchannel_test.go pins the attach arm's channel delivery — the §4.3 half the
// attach branch never had until per-entry delivery existed. Three pins:
//
//   - the WRITE that delivers (the same writeUserEnvFile the fresh path performs,
//     against the live-mounted file the exec'd boot hydrates);
//   - the credential PRE-FLIGHT, moved to this arm with the delivery;
//   - the REFUSAL for a jail that cannot receive the delivery (a pre-change jail, whose
//     frozen environment this write cannot override, and a pre-gate jail, whose launchers
//     source no per-agent file) — now the contract gate's (contracttags.go), whose own
//     disposition tests are in contracttags_test.go.
//
// The attach tests that need the exec drive it against a fake runtime first on PATH
// (attachToExec); attachExisting's call to the delivery is also AST-pinned, because
// deleting it passes every test that calls the callee directly.

import (
	"bytes"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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
// the persistent profile table, as opposed to the -p flag fields on Options,
// which the pre-change check must treat differently (typed vs config).
func configSelects(cfg *jsonx.OrderedMap, cli, profile string) {
	profiles := jsonx.NewOrderedMap()
	profiles.Set(cli, profile)
	cfg.Set("profile", profiles)
}

// TestAttachDeliversTheChannelFile is the positive pin on the WRITE: a post-change
// jail (no YOLO_PROVIDERS in its frozen env) with the zai profile selected gets the
// channel section into its live-mounted yolo-user-env.sh, and the disclosure line
// prints beside it — the same sentence the fresh launch prints, because this entry
// delivers the same thing.
func TestAttachDeliversTheChannelFile(t *testing.T) {
	packs := zaiSelected(t)
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv,
		packs, hydratedKey(), func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })

	rc := o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel)
	if rc != 0 {
		t.Fatalf("delivery refused a healthy attach: rc=%d\n%s", rc, stderr.String())
	}
	file := filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the attach never wrote the channel file: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "export YOLO_USE_PROFILES=") {
		t.Errorf("channel file missing the selection table:\n%s", s)
	}
	// The provider environment claude's profile composed is CLAUDE's alone (the credential
	// gate, OQ-CN6): the attach writes it into claude's own env file, never the shared one.
	for _, gone := range []string{"ANTHROPIC_AUTH_TOKEN", "tok-9"} {
		if strings.Contains(s, gone) {
			t.Errorf("the shared channel file carries %s — every process would see it:\n%s", gone, s)
		}
	}
	ab, err := os.ReadFile(filepath.Join(paths.WorkspaceHomeState(o.Workspace), agentEnvStateDir, "claude.sh"))
	if err != nil {
		t.Fatalf("the attach never wrote claude's own env file: %v", err)
	}
	for _, want := range []string{
		"export ANTHROPIC_BASE_URL=${ANTHROPIC_BASE_URL:-'https://api.z.ai/api/anthropic'}",
		"export ANTHROPIC_AUTH_TOKEN=${ANTHROPIC_AUTH_TOKEN:-'tok-9'}",
	} {
		if !strings.Contains(string(ab), want) {
			t.Errorf("claude's env file missing %s:\n%s", want, ab)
		}
	}
	if out := stderr.String(); !strings.Contains(out, `Profile zai: declared by zai; claude → provider "zai", on its "anthropic" endpoint`) {
		t.Errorf("the attach must print where the selection landed:\n%s", out)
	}
	if out := stderr.String(); !strings.Contains(out, "ZAI_API_KEY (provider zai): claude only") {
		t.Errorf("the attach must disclose the credential gate's scope, as the fresh launch does:\n%s", out)
	}
}

func TestAttachDisclosesAnInheritedParentJailPointer(t *testing.T) {
	packs := awsAuthSelected(t)
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, emptyEnv(), func(o *Options, cfg *jsonx.OrderedMap) {
		o.runtime = "podman"
		o.ProfileName = "bedrock"
		inAJail(o)
		o.Getenv = launchingJailEnv
		served, _ := awsAuthServedConfig(t, packs).Get("loopholes")
		cfg.Set("loopholes", served)
	})

	rc, restarted, execed := attachToExec(t, o, cfg, packs, channel)
	if rc != 0 || restarted || !execed {
		t.Fatalf("a healthy inherited-pointer attach did not execute: rc=%d restarted=%v execed=%v\n%s",
			rc, restarted, execed, stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got,
		"aws-auth: the nested jail uses this jail's own Bedrock credentials (narrowed by the host; no daemon started)") {
		t.Errorf("the successful attach delivered an inherited pointer without disclosing it:\n%s", got)
	}
	if strings.Contains(stderr.String(), parentToken) {
		t.Error("the attach disclosure printed the inherited token")
	}
}

// currentJailEnv is the frozen environment of a jail THIS yolo launched: no wire tables (they
// cross in the file), and the contract tags every current launch freezes in
// (entrypoint.ContractTagsEnv), which tell an attach what the jail can receive — the
// per-agent env files among them.
var currentJailEnv = "YOLO_VERSION=9.9.9-test\n" + entrypoint.ContractTagsEnv + "=" + launchContractTagsValue() + "\n"

// awsAdapterJailEnv is currentJailEnv for a jail whose launch STARTED aws-auth's adapter — its
// frozen YOLO_JAIL_DAEMONS names it, as a launch that selected `bedrock` froze it (OQ-CN7 (b)).
var awsAdapterJailEnv = currentJailEnv +
	`YOLO_JAIL_DAEMONS=[{"name":"aws-auth","cmd":["yolo-jaild","aws-credential-adapter"],"restart":"on-failure"}]` + "\n"

// gateEraJailEnv is a jail launched by the credential gate's first build, before the contract
// tags: it froze the legacy per-agent env marker (entrypoint.AgentEnvFilesEnv) instead, and
// must still count as a jail whose launchers source the per-agent files.
const gateEraJailEnv = "YOLO_VERSION=0.10.0+500\nYOLO_AGENT_ENV_FILES=1\n"

// preGateEnv is the frozen environment of a jail launched after per-entry delivery and
// before the credential gate: no frozen tables, no tags and no per-agent env marker — that
// yolo bound no agent-env directory and wrote launchers that source none.
const preGateEnv = "YOLO_VERSION=0.10.0\n"

// attachToExec drives the real attachExisting with a fake runtime first on PATH, so an
// attach that goes ahead runs through to its exec without a container. It reports the
// attach's result and whether the exec happened; the fixture's Exec still answers inspect.
func attachToExec(t *testing.T, o *Options, cfg *jsonx.OrderedMap, packs []*packload.Pack,
	channel *packChannel) (rc int, restarted, execed bool) {
	t.Helper()
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "execed")
	// A shell builtin, since PATH holds nothing but this directory.
	script := "#!/bin/sh\n: > " + shquote.Quote(record) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	rc, restarted = o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false, nil)
	_, err := os.Stat(record)
	return rc, restarted, err == nil
}

// A pre-gate jail cannot receive what the credential gate scopes to one agent, so an attach
// whose selection scopes anything refuses there, typed or not, before any write — without a
// terminal to ask a restart on (the gate's disposition, contracttags.go). Delivering anyway
// would rewrite the live shared file without claude's shape vars and zai key (the gate keeps
// them out of it), write claude a file that jail never sources, and print "ZAI_API_KEY
// (provider zai): claude only" while claude starts pointed at Anthropic first-party. The
// config-only arm used to warn and deliver nothing (CN-D18), the silent ride-along the
// attach-skew ruling forbids (OQ-SK1).
func TestAttachRefusesAPreGateJailItCannotDeliverTo(t *testing.T) {
	packs := zaiSelected(t)
	for _, tc := range []struct {
		name string
		tune func(*Options, *jsonx.OrderedMap)
	}{
		{"typed selection", func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" }},
		{"config selection", func(_ *Options, cfg *jsonx.OrderedMap) { configSelects(cfg, "claude", "zai") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, preGateEnv, packs, hydratedKey(), tc.tune)
			envFile, before := seedLiveChannelFile(t, o)

			rc, restarted, execed := attachToExec(t, o, cfg, packs, channel)
			if rc != 1 || restarted || execed {
				t.Fatalf("a pre-gate jail this entry cannot deliver to must refuse: rc=%d restarted=%v "+
					"execed=%v\n%s", rc, restarted, execed, stderr.String())
			}
			out := stderr.String()
			for _, want := range []string{"Refusing to attach", "agent-env-files", "claude (profile zai)",
				"ZAI_API_KEY", "'yolo stop'", AllowAttachSkewEnv} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal must name %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "claude only") {
				t.Errorf("the refusal must not disclose a scope this jail cannot receive:\n%s", out)
			}
			// Neither live file, the shared one or claude's own, is touched.
			assertLiveChannelFileUnchanged(t, envFile, before)
		})
	}
}

// A pre-gate jail still reads the shared file on every entry, so an attach that scopes
// nothing to any agent needs nothing it lacks and delivers as usual. So does an attach whose
// inspect proved nothing, which is treated as a current jail; and so does a jail the gate's
// first build launched, which carries the legacy marker instead of the tags.
func TestAttachDeliversWhenNothingItNeedsIsMissing(t *testing.T) {
	packs := zaiSelected(t)
	for _, tc := range []struct {
		name, env string
		selects   bool
	}{
		{"pre-gate jail, nothing scoped to any agent", preGateEnv, false},
		{"an inspect that returned nothing", "", true},
		{"a gate-era jail with the legacy marker", gateEraJailEnv, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, tc.env, packs, hydratedKey(), nil)
			if tc.selects {
				configSelects(cfg, "claude", "zai")
				channel = channelFor(t, o, cfg, packs, hydratedKey())
			}
			rc, restarted, execed := attachToExec(t, o, cfg, packs, channel)
			if rc != 0 || restarted || !execed {
				t.Fatalf("rc=%d restarted=%v execed=%v\n%s", rc, restarted, execed, stderr.String())
			}
			if out := stderr.String(); strings.Contains(out, "Refusing") || strings.Contains(out, "lacks") {
				t.Errorf("nothing this attach needs is missing, so nothing is refused or disclosed:\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")); err != nil {
				t.Errorf("the channel must still be delivered: %v", err)
			}
		})
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
		// The bridge, as ResolveNeeds would join it for a claude launch beside cerebras.
		// Since the bridged address is the ADAPTER's declaration, its pack is what makes
		// `-p cerebras` resolvable for claude at all — without it the composition refuses
		// one layer before the attach behaviour under test.
		officialPack(t, "wire-bridge"),
	}
}

// A jail launched before the channel moved onto the file cannot take a DIFFERENT
// selection — its old hydrate lets the frozen environment beat the file — so an attach
// selecting one refuses rather than run on the jail's launch-time providers, typed or not.
// The remedy names the two-command restart series ('yolo stop', then an ordinary launch) —
// the old --new flag force-removed the RUNNING container and its sessions, was recommended
// by an earlier cut of this message, and is removed outright now. The config-only arm used
// to warn and proceed, the ride-along OQ-SK1 forbids.
func TestAttachRefusesADifferingSelectionOnAPreChangeJail(t *testing.T) {
	packs := preChangePacks(t)
	for _, tc := range []struct {
		name string
		tune func(*Options, *jsonx.OrderedMap)
	}{
		{"typed selection", func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "cerebras" }},
		{"config selection", func(_ *Options, cfg *jsonx.OrderedMap) { configSelects(cfg, "claude", "cerebras") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, preChangeEnv, packs, hydratedKey(), tc.tune)

			rc, restarted, execed := attachToExec(t, o, cfg, packs, channel)
			if rc != 1 || restarted || execed {
				t.Fatalf("a differing selection a pre-change jail cannot take must refuse: rc=%d "+
					"restarted=%v execed=%v\n%s", rc, restarted, execed, stderr.String())
			}
			out := stderr.String()
			for _, want := range []string{"Refusing to attach", "entry-channel", "claude=cerebras",
				"the jail keeps claude=zai", "'yolo stop'"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal must name %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "--new") {
				t.Errorf("the remedy must not name the removed --new flag:\n%s", out)
			}
			// And it refuses BEFORE delivering: nothing was written into the workspace.
			if _, err := os.Stat(filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")); !os.IsNotExist(err) {
				t.Errorf("a refused attach must not write the channel file")
			}
		})
	}
}

// TestAttachToAPreChangeJailWithMatchingSelectionIsSilent: the plain re-entry. A
// pre-change jail whose frozen selection matches what this entry selects (or where
// this entry selects nothing) must behave exactly as attaches did before per-entry
// delivery existed — no refusal, no warning, no delivery. The first cut of the
// pre-change check refused every attach whose table was merely NON-EMPTY, which
// broke the daily 'yolo -- <cmd>' of any config carrying a persistent
// profile (measured on a live jail, 2026-09-05).
func TestAttachToAPreChangeJailWithMatchingSelectionIsSilent(t *testing.T) {
	packs := preChangePacks(t)
	for _, tc := range []struct {
		name string
		tune func(*Options, *jsonx.OrderedMap)
	}{
		{"config selection matches the frozen one", func(o *Options, cfg *jsonx.OrderedMap) {
			configSelects(cfg, "claude", "zai")
		}},
		{"typed selection matches the frozen one", func(o *Options, _ *jsonx.OrderedMap) {
			o.UseProfiles = map[string]string{"claude": "zai"}
		}},
		{"no selection at all", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, preChangeEnv, packs, hydratedKey(), tc.tune)
			rc, restarted, execed := attachToExec(t, o, cfg, packs, channel)
			if rc != 0 || restarted || !execed {
				t.Fatalf("a plain re-entry into a pre-change jail must go ahead: rc=%d restarted=%v "+
					"execed=%v\n%s", rc, restarted, execed, stderr.String())
			}
			if out := stderr.String(); strings.Contains(out, "Refusing") || strings.Contains(out, "lacks") {
				t.Errorf("a plain re-entry must be silent:\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")); !os.IsNotExist(err) {
				t.Errorf("a plain re-entry into an old jail must not write the channel file")
			}
		})
	}
}

// TestAttachRunsTheCredentialPreflight: the attach arm delivers environment, so an
// unhydratable key refuses HERE too — a session that would start with
// ANTHROPIC_BASE_URL and no token is §6.1's mysterious first-API-call failure. The
// contract gate is out of the way (a current jail), so the refusal names the variable,
// not the jail.
func TestAttachRunsTheCredentialPreflight(t *testing.T) {
	packs := zaiSelected(t)
	// No hydrated key and no ZAI_API_KEY in the invoking environment.
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv,
		packs, emptyEnv(), func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })

	rc, _ := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false, nil)
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

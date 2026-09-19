package run

// awschannels_test.go pins the ninth pre-flight at BOTH ends: what it says about a
// composed channel, and that every arm of the launch still asks it.
//
// The second half is the point. `awschain.ExclusivityRefusal` has its own unit tests
// (internal/awschain), and none of them would notice if this package stopped calling
// it — the callee-pinned/call-site-unpinned shape AGENTS.md names, which this repo has
// shipped repeatedly. There are THREE call sites, one per way a launch delivers an
// environment, and each is pinned by a test named below:
//
//	the macos-user arm (run.go, Run)     TestAWSChannelExclusivityRefusesTheMacosUserLaunch
//	runContainer (run.go)                TestFreshLaunchChecksAWSCredentialChannelsFirst
//	deliverChannelOnAttach (run.go)      TestAttachRefusesTwoAWSCredentialChannels
//
// Two are behavioral (the refusal is observable through Run and through attachExisting);
// the container one is a source pin, for the reason its sibling
// TestFreshLaunchChecksProviderCredentialsOnTheAssembledEnv gives — runContainer starts a
// real container, so a unit test has no other witness.

import (
	"bytes"
	"go/ast"
	"go/token"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/awschain"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bothAWSArms is the conflicting environment, in the one delivery channel every arm
// shares: the shell yolo was launched from. The pack-shipped spelling of the same
// conflict is exercised by TestAWSChannelSeesThePackShippedPointer below; here the
// simplest possible delivery keeps each call-site pin about the CALL SITE.
func bothAWSArms(name string) string {
	switch name {
	case awschain.BearerTokenVar:
		return "sk-bedrock-frozen"
	case awschain.PointerVar:
		return "http://127.0.0.1:1461/credentials"
	}
	return ""
}

// awsNativeLaunch drives Run() to the macos-user arm over the shipped claude pack with
// no profile selected — so nothing but the AWS rule can refuse it — and the given
// environment in the invoking shell.
func awsNativeLaunch(t *testing.T, env func(string) string) (*Options, *bytes.Buffer) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"claude"}
	o.Getenv = func(name string) string {
		if name == "YOLO_RUNTIME" {
			return "macos-user"
		}
		return env(name)
	}
	return o, &stderr
}

// TestAWSChannelExclusivityRefusesTheMacosUserLaunch is CALL SITE 1, asserted at the
// dispatch: the handler must never be reached. This backend is where the credential
// pre-flight was missing for a whole release (it lived only in runContainer, below the
// return), so a rule added to one arm and not the other is this file's own history.
func TestAWSChannelExclusivityRefusesTheMacosUserLaunch(t *testing.T) {
	o, stderr := awsNativeLaunch(t, bothAWSArms)
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string, _ macosuser.HostContext, _ bool,
		_ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		reached = true
		return 0
	}
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1: a native launch carrying both AWS credential arms must "+
			"refuse\nstderr:\n%s", rc, stderr.String())
	}
	if reached {
		t.Error("the refused launch still reached the macos-user handler — the pre-flight " +
			"must run BEFORE the backend is dispatched, not after the sandbox started")
	}
	got := stderr.String()
	for _, want := range []string{awschain.BearerTokenVar, awschain.PointerVar} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q, or the reader cannot tell which to drop:\n%s",
				want, got)
		}
	}
}

// TestAWSChannelExclusivityLetsAOneArmedNativeLaunchThrough is the control for the test
// above, and it is not decoration: a rule that refused the bearer ALONE would refuse
// every jail using AWS_BEARER_TOKEN_BEDROCK through env_sources, which §8 of
// docs/design/sso-backed-bedrock.md promises keeps working unchanged.
func TestAWSChannelExclusivityLetsAOneArmedNativeLaunchThrough(t *testing.T) {
	o, stderr := awsNativeLaunch(t, func(name string) string {
		if name == awschain.BearerTokenVar {
			return "sk-bedrock-frozen"
		}
		return ""
	})
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string, _ macosuser.HostContext, _ bool,
		_ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		reached = true
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0: the pre-existing env_sources bearer is not a conflict"+
			"\nstderr:\n%s", rc, stderr.String())
	}
	if !reached {
		t.Error("a one-armed launch never reached the backend — the exclusivity rule is " +
			"refusing the configuration it was written to leave alone")
	}
}

// TestFreshLaunchChecksAWSCredentialChannelsFirst is CALL SITE 2. Its sibling
// TestFreshLaunchChecksProviderCredentialsOnTheAssembledEnv states why this shape is a
// source pin rather than a behavioral one, and the two orderings asserted here are the
// design's, not this test's:
//
//   - AFTER assembleRunCmd, because the `-e K=V` argv is half of what "delivered" means
//     on this backend (a pack-shipped loophole's jail_env reaches the jail only there);
//   - BEFORE checkProviderCredentials, because two channels is a WRONG answer and a
//     missing credential is a MISSING one, and the wrong answer has no escape hatch to
//     weigh;
//   - BEFORE startLoopholesDisclosed, so a refusal never has to unwind a host daemon it
//     started.
func TestFreshLaunchChecksAWSCredentialChannelsFirst(t *testing.T) {
	const (
		assemble = "assembleRunCmd"
		awsCheck = "checkAWSCredentialChannels"
		creds    = "checkProviderCredentials"
		daemons  = "startLoopholesDisclosed"
	)
	fn := methodDecl(t, "run.go", "runContainer")

	pos := map[string]token.Pos{}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, seen := pos[sel.Sel.Name]; !seen {
			pos[sel.Sel.Name] = call.Pos()
		}
		return true
	})

	if _, ok := pos[awsCheck]; !ok {
		t.Fatalf("runContainer no longer calls %s. internal/awschain's unit tests still pass "+
			"and so do this file's other two call-site pins, so a container launch carrying "+
			"BOTH AWS credential arms would start a jail that authenticates with a credential "+
			"frozen at launch while the refreshing half sat idle — the silent wrong answer "+
			"docs/design/sso-backed-bedrock.md §8 forbids. If the seam moved, move this "+
			"assertion with it rather than deleting it.", awsCheck)
	}
	if pos[awsCheck] < pos[assemble] {
		t.Errorf("runContainer calls %s BEFORE %s: the check answers against the assembled -e "+
			"argv, so it cannot run before that argv exists", awsCheck, assemble)
	}
	if _, ok := pos[creds]; ok && pos[awsCheck] > pos[creds] {
		t.Errorf("runContainer calls %s AFTER %s: two delivered channels is a wrong answer "+
			"and a missing credential is a missing one, so the wrong answer is refused first",
			awsCheck, creds)
	}
	if _, ok := pos[daemons]; ok && pos[awsCheck] > pos[daemons] {
		t.Errorf("runContainer calls %s AFTER %s: a refusal must land before any host-side "+
			"daemon a refusal would have to clean up", awsCheck, daemons)
	}
}

// TestAttachRefusesTwoAWSCredentialChannels is CALL SITE 3, driven through
// attachExisting — the arm that delivers an environment into a jail that is ALREADY
// RUNNING. It refuses there for the same reason the fresh path does: the channel file
// this arm writes is what the next agent process reads, so an attach that delivered both
// arms would hand a live jail the silent wrong answer without any launch banner in sight.
func TestAttachRefusesTwoAWSCredentialChannels(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude")}
	o, cfg, channel, stderr := attachFixture(t, "YOLO_VERSION=9.9.9-test\n",
		packs, emptyEnv(), func(o *Options, _ *jsonx.OrderedMap) {
			o.Getenv = bothAWSArms
		})

	rc := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false)
	if rc != 1 {
		t.Fatalf("an attach delivering both AWS credential arms must refuse, rc=%d\nstderr:\n%s",
			rc, stderr.String())
	}
	got := stderr.String()
	for _, want := range []string{awschain.BearerTokenVar, awschain.PointerVar} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q:\n%s", want, got)
		}
	}
}

// --- what the check says about a composed channel (the input assembly) ---

// TestAWSChannelSeesThePackShippedPointer is the REALISTIC conflict, and the one that
// proves the predicate is keyed on the variable rather than on a pack name: the pointer
// arrives from packs/aws-auth's profile-gated `kind: "env"` contribution, the bearer from
// the shell, and the refusal names the pack contribution as the pointer's origin.
//
// It also covers the negative that makes `packs/aws-auth` usable at all: selecting the
// pack WITHOUT the profile that gates its pointer delivers nothing, so there is no silent
// winner to refuse over.
func TestAWSChannelSeesThePackShippedPointer(t *testing.T) {
	selected := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "aws-auth")}

	t.Run("the bedrock profile delivers the pointer", func(t *testing.T) {
		o := retireOptions(t, discardBuf())
		o.Getenv = bothAWSArms
		o.ProfileName = "bedrock"

		cfg := newConfig()
		lines := o.checkAWSCredentialChannels(channelFor(t, o, cfg, selected, emptyEnv()), nil)
		if len(lines) == 0 {
			t.Fatal("aws-auth's pointer plus a bearer in the shell must refuse — this is the " +
				"exact configuration docs/design/sso-backed-bedrock.md §8 forbids")
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, `kind: "env"`) {
			t.Errorf("the refusal must name the pack contribution as the pointer's origin, "+
				"not merely the variable:\n%s", joined)
		}
	})

	t.Run("the same packs without the profile deliver nothing", func(t *testing.T) {
		o := retireOptions(t, discardBuf())
		o.Getenv = func(name string) string {
			if name == awschain.BearerTokenVar {
				return "sk-bedrock-frozen"
			}
			return ""
		}
		cfg := newConfig()
		if lines := o.checkAWSCredentialChannels(channelFor(t, o, cfg, selected, emptyEnv()), nil); len(lines) != 0 {
			t.Errorf("selecting aws-auth without the profile that gates its pointer refused a "+
				"launch that delivers ONE arm:\n%s", strings.Join(lines, "\n"))
		}
	})
}

// TestAWSChannelReadsTheAssembledArgv: the container arm's fifth source. A pack-shipped
// loophole's jail_env reaches the jail on the `-e K=V` argv and nowhere else, so a check
// that read only the composed channel would be blind to exactly the delivery this feature
// ships.
func TestAWSChannelReadsTheAssembledArgv(t *testing.T) {
	o := retireOptions(t, discardBuf())
	o.Getenv = func(name string) string {
		if name == awschain.BearerTokenVar {
			return "sk-bedrock-frozen"
		}
		return ""
	}
	packs := []*packload.Pack{officialPack(t, "claude")}
	channel := channelFor(t, o, newConfig(), packs, emptyEnv())

	if lines := o.checkAWSCredentialChannels(channel, nil); len(lines) != 0 {
		t.Fatalf("the bearer alone must not refuse:\n%s", strings.Join(lines, "\n"))
	}
	argv := map[string]string{awschain.PointerVar: "http://127.0.0.1:1461/credentials"}
	if lines := o.checkAWSCredentialChannels(channel, argv); len(lines) == 0 {
		t.Error("a pointer that reaches the jail only through the assembled argv was not seen")
	}
}

// TestAWSChannelIsSilentWithoutAComposedChannel: every arm calls this with whatever it
// has, and the macos-user arm can reach the call with a nil channel on a launch that
// composed nothing. Silence, not a panic.
func TestAWSChannelIsSilentWithoutAComposedChannel(t *testing.T) {
	o := retireOptions(t, discardBuf())
	o.Getenv = bothAWSArms
	if lines := o.checkAWSCredentialChannels(nil, nil); lines != nil {
		t.Errorf("a nil channel produced a refusal: %v", lines)
	}
}

// TestAWSChannelTreatsAnEmptyValueAsUndelivered mirrors the credential pre-flight's own
// rule: the launch drops an empty value rather than composing an empty token, so an empty
// pointer is not a second channel — there would be nothing for an SDK to dial.
func TestAWSChannelTreatsAnEmptyValueAsUndelivered(t *testing.T) {
	o := retireOptions(t, discardBuf())
	o.Getenv = func(name string) string {
		if name == awschain.BearerTokenVar {
			return "sk-bedrock-frozen"
		}
		return ""
	}
	packs := []*packload.Pack{officialPack(t, "claude")}
	channel := channelFor(t, o, newConfig(), packs, emptyEnv())
	argv := map[string]string{awschain.PointerVar: ""}
	if lines := o.checkAWSCredentialChannels(channel, argv); len(lines) != 0 {
		t.Errorf("an empty pointer counted as a delivered channel:\n%s", strings.Join(lines, "\n"))
	}
}

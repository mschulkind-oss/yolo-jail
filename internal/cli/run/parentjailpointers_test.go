package run

// parentjailpointers_test.go pins docs/design/sso-backed-bedrock.md SSO-D2 to SSO-D5 on the
// launcher: a podman launch from inside a jail whose environment carries aws-auth's pointer starts
// neither of aws-auth's daemons and hands the nested Bedrock agent the launching jail's own
// pointer and region; with the pointer absent, or from the host, the launch is today's. Driven
// through the real composition over the shipped packs (jailDaemonsFor, composePackChannel,
// deliverChannel, the agent's real env file), the spawn's own selection (plannedLoopholeNames,
// which the keeper checks the spawn against), and the argv's endpoint emission. No AWS call is
// made, and no value below is a real credential.

import (
	"bytes"
	"go/ast"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const (
	parentURI    = "http://127.0.0.1:52001/credentials" // a port the launching jail picked, as one sharing its own launcher's namespace does
	parentToken  = "parent-jail-caller-token-stand-in"
	parentRegion = "eu-north-1"
)

// launchingJailEnv is the environment of a shell in a jail whose own launch served aws-auth's
// pointer: the two pointer variables and the region, as this repo's jail carries them.
func launchingJailEnv(name string) string {
	switch name {
	case "AWS_CONTAINER_CREDENTIALS_FULL_URI":
		return parentURI
	case "AWS_CONTAINER_AUTHORIZATION_TOKEN":
		return parentToken
	case "AWS_REGION":
		return parentRegion
	}
	return ""
}

// inAJail makes o a launcher running inside a container, the probe the assembler forces
// `--net=host` on (inContainer).
func inAJail(o *Options) {
	o.PathExists = func(p string) bool { return p == "/run/.containerenv" }
}

// nestedAWSLaunch is one podman launch of claude on bedrock with aws-auth enabled and no provider
// region, tuned by tune, delivered into a jail home: the agent's env file as claude reads it, the
// composed payload's daemon names, the host services the spawn would start, the argv's
// host-service emission, and what the launch said.
type nestedAWSLaunch struct {
	claude  map[string]string
	payload map[string]bool
	planned []string
	mounts  string
	said    string
	minted  string
}

func launchNestedAWS(t *testing.T, tune func(*Options)) nestedAWSLaunch {
	t.Helper()
	var out nestedAWSLaunch
	var cfgSeen *jsonx.OrderedMap
	var opts *Options
	jail, channel, said := launchGateJailWith(t, []string{"claude", "aws-auth", "bedrock"},
		func(packs []*packload.Pack) *jsonx.OrderedMap {
			cfg := bareConfig()
			served, _ := awsAuthServedConfig(t, packs).Get("loopholes")
			cfg.Set("loopholes", served)
			cfgSeen = cfg
			return cfg
		}, emptyEnv(), func(o *Options) {
			o.runtime = "podman"
			o.UseProfiles = map[string]string{"claude": "bedrock"}
			if tune != nil {
				tune(o)
			}
			opts = o
		})
	out.claude = jail.agentEnv("claude")
	out.said = said
	out.minted = channel.callerTokens[paths.ServiceCallerTokenEnv("aws-auth")]
	var packs []*packload.Pack
	for _, name := range []string{"claude", "aws-auth", "bedrock"} {
		packs = append(packs, officialPack(t, name))
	}
	out.payload = map[string]bool{}
	for _, s := range opts.jailDaemonsFor(cfgSeen, "podman", packs) {
		out.payload[s.Name] = true
	}
	out.planned = opts.plannedLoopholeNames("podman", cfgSeen)
	out.mounts = strings.Join(opts.hostServicesMountArgs("podman", "yolo-ws-abcd1234", cfgSeen), " ")
	var stderr bytes.Buffer
	opts.Stderr = &stderr
	opts.noteParentJailPointers(cfgSeen, channel)
	out.said += stderr.String()
	return out
}

// SSO-D2: in a jail, on podman, with the launching jail's pointer in the environment, the nested
// launch hands claude that pointer verbatim, mints no caller token of its own, runs no aws-auth
// jail daemon, starts no aws-auth host daemon (the keeper's plan agrees), emits no endpoint for
// the witness to wait on, and says so in one line. SSO-D4: claude's provider names no region, so
// the launching jail's region comes with the pointer.
func TestANestedPodmanLaunchInheritsTheLaunchingJailsPointer(t *testing.T) {
	got := launchNestedAWS(t, func(o *Options) {
		inAJail(o)
		o.Getenv = launchingJailEnv
	})
	if v := got.claude["AWS_CONTAINER_CREDENTIALS_FULL_URI"]; v != parentURI {
		t.Errorf("claude's AWS_CONTAINER_CREDENTIALS_FULL_URI = %q, want the launching jail's %q", v, parentURI)
	}
	if v := got.claude["AWS_CONTAINER_AUTHORIZATION_TOKEN"]; v != parentToken {
		t.Errorf("claude's AWS_CONTAINER_AUTHORIZATION_TOKEN is not the launching jail's token (got %d bytes)", len(v))
	}
	if v := got.claude["AWS_REGION"]; v != parentRegion {
		t.Errorf("claude's AWS_REGION = %q, want the launching jail's %q, which came with the pointer", v, parentRegion)
	}
	if got.minted != "" {
		t.Error("the nested launch minted an aws-auth caller token, for a daemon it must not run")
	}
	if got.payload["aws-auth"] {
		t.Error("the nested payload runs the aws-auth jail daemon; the launching jail's serves the pointer")
	}
	if slices.Contains(got.planned, "aws-auth") {
		t.Errorf("the nested launch would start the aws-auth host daemon (planned %v)", got.planned)
	}
	if strings.Contains(got.mounts, "YOLO_SERVICE_AWS_AUTH_ENDPOINT") {
		t.Errorf("the argv names an aws-auth endpoint nothing will publish: %s", got.mounts)
	}
	for _, want := range []string{
		"aws-auth: the nested jail uses this jail's own Bedrock credentials (narrowed by the host; no daemon started)",
		"Region: AWS_REGION=" + parentRegion + " for claude", "the launching jail's AWS_REGION",
	} {
		if !strings.Contains(got.said, want) {
			t.Errorf("the launch must say %q:\n%s", want, got.said)
		}
	}
	if strings.Contains(got.said, parentToken) {
		t.Error("the launch printed the launching jail's token")
	}
}

// todaysAWSLaunch asserts the launch is the one this repo shipped before SSO-D2: the jail daemon
// in the payload behind a caller token of this launch's own, the host daemon started, its
// endpoint emitted, and nothing said about a parent.
func todaysAWSLaunch(t *testing.T, got nestedAWSLaunch) {
	t.Helper()
	if !got.payload["aws-auth"] {
		t.Error("the aws-auth jail daemon left the payload")
	}
	if !slices.Contains(got.planned, "aws-auth") {
		t.Errorf("the aws-auth host daemon is not started (planned %v)", got.planned)
	}
	if !strings.Contains(got.mounts, "YOLO_SERVICE_AWS_AUTH_ENDPOINT") {
		t.Errorf("the argv names no aws-auth endpoint: %s", got.mounts)
	}
	if got.minted == "" || got.claude["AWS_CONTAINER_AUTHORIZATION_TOKEN"] != got.minted {
		t.Error("claude's token is not the one this launch minted for its own adapter")
	}
	if v := got.claude["AWS_CONTAINER_CREDENTIALS_FULL_URI"]; v == parentURI || v == "" {
		t.Errorf("claude's AWS_CONTAINER_CREDENTIALS_FULL_URI = %q, want this launch's adapter", v)
	}
	if v := got.claude["AWS_REGION"]; v == parentRegion {
		t.Errorf("claude's AWS_REGION is the launching jail's %q, with no inherited pointer to bring it", v)
	}
	if strings.Contains(got.said, "no daemon started") {
		t.Errorf("the launch claims an inheritance it did not make:\n%s", got.said)
	}
}

// A nested podman launch whose environment does not carry the whole pointer runs aws-auth as
// before, refusal included: half a pointer is no pointer.
func TestANestedLaunchWithoutTheLaunchingJailsPointerIsTodays(t *testing.T) {
	for name, env := range map[string]func(string) string{
		"no pointer": func(string) string { return "" },
		"no token": func(k string) string {
			if k == "AWS_CONTAINER_AUTHORIZATION_TOKEN" {
				return ""
			}
			return launchingJailEnv(k)
		},
	} {
		t.Run(name, func(t *testing.T) {
			todaysAWSLaunch(t, launchNestedAWS(t, func(o *Options) {
				inAJail(o)
				o.Getenv = env
			}))
		})
	}
}

// From the host the launching shell's variables are no jail's pointer, and no loopback is shared:
// the launch is today's whatever that shell holds.
func TestALaunchFromTheHostInheritsNothing(t *testing.T) {
	todaysAWSLaunch(t, launchNestedAWS(t, func(o *Options) {
		o.Getenv = launchingJailEnv
	}))
}

// Only podman is provably forced onto the launching jail's namespace.
func TestOnlyANestedPodmanLaunchInherits(t *testing.T) {
	o := goldenOptions(t.TempDir(), packHome(t))
	inAJail(o)
	o.Getenv = launchingJailEnv
	packs := []*packload.Pack{officialPack(t, "aws-auth")}
	set := loopholes.NewHostSet(cfgMap(awsAuthServedConfig(t, packs), "loopholes"))
	if got := o.parentJailPointers("podman", set); got["aws-auth"] == nil {
		t.Fatalf("a nested podman launch inherits nothing: %v", got)
	}
	for _, rt := range []string{"container", "macos-user", ""} {
		if got := o.parentJailPointers(rt, set); len(got) != 0 {
			t.Errorf("runtime %q inherited %v", rt, len(got))
		}
	}
}

// SSO-D5: the fetched model list is not asked of a service whose pointer this launch inherits,
// whose narrowed credential cannot list models; the launch says and refuses what a failed fetch
// makes it. It fails with composeFetchedLists' inheritance branch removed (the seam is then asked).
func TestANestedLaunchsModelListFetchFailsAsARealFetchFailure(t *testing.T) {
	o, cfg, packs, _ := bareBedrockLaunch(t)
	o.runtime = "podman"
	inAJail(o)
	o.Getenv = launchingJailEnv
	asked := 0
	o.FetchModelList = func(ModelListRequest) awsauthdaemon.ModelListAnswer {
		asked++
		return awsauthdaemon.ModelListAnswer{Source: "fetched"}
	}
	_, err := o.composePackChannel(cfg, packs, jsonx.NewOrderedMap())
	if asked != 0 {
		t.Errorf("the nested launch asked the model-list service %d times", asked)
	}
	if err == nil {
		t.Fatal("copilot has nothing to start on and the launch composed anyway")
	}
	for _, want := range []string{`copilot has no model to start on for profile "bbp"`,
		"yolo could not fetch the region's list: this nested launch takes its aws-auth credentials from " +
			"the launching jail", `"providers.bb.models"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%s", want, err)
		}
	}
}

// The disclosure is printed on the fresh container path, so deleting its call fails here
// (runContainer starts a real container, so the call graph is the witness, as for
// noteUnstartedProfileDaemons).
func TestTheInheritedPointerDisclosureIsPrintedByTheFreshContainerPath(t *testing.T) {
	found := false
	ast.Inspect(funcDeclIn(t, "run.go", "runContainer"), func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "noteParentJailPointers" {
				found = true
			}
		}
		return true
	})
	if !found {
		t.Error("runContainer never prints noteParentJailPointers — an inherited pointer would go unsaid")
	}
}

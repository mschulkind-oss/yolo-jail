package run

// envdisclosure_test.go pins what the LAUNCH BANNER says about a pack's `kind: "env"`
// contribution (notePackHostAccess, which reads the footprint's env claims).
//
// The subject is the profile gate. A gated contribution sets its variables only while its
// profile is active, and the banner is printed per declaration, not per launch — so the line
// has to carry the gate, or it announces a variable as set on launches that do not set it.
// packs/aws-auth's pointer is the case that shipped wrong: the footprint claimed it twice, once
// with the gate and once without, and the banner printed the one without.

import (
	"bytes"
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestLaunchBannerQualifiesAGatedEnvVariable is the golden for the shipped aws-auth pack's
// read disclosure: one line per env variable, each carrying its `bedrock` gate. The banner reads the
// footprint (disclosedClaims), so this fails if the footprint's env loop starts claiming a
// gated contribution unconditionally again, or if the banner stops printing env claims.
func TestLaunchBannerQualifiesAGatedEnvVariable(t *testing.T) {
	var stderr bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &stderr
	o.Stdout = discardBuf()
	packs := []*packload.Pack{officialPack(t, "aws-auth")}
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	loopholes.SetPackModules(packLoopholeModules(packs))
	// The served set a bridged podman launch composes (jailDaemonsFor, servedDaemons) with the
	// loophole enabled, as a Bedrock launch enables it: the AWS adapter at its declared address,
	// which is what the banner names for the pointer.
	cfg := bareConfig()
	block, entry := jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	block.Set("aws-auth", entry)
	cfg.Set("loopholes", block)
	// An agent whose profile selects what the adapter serves, or the payload leaves the adapter
	// out (OQ-CN7 (b)); the banner reads aws-auth's declarations alone.
	o.UseProfiles = map[string]string{"claude": "bedrock"}
	withAgent := append([]*packload.Pack{officialPack(t, "claude"), officialPack(t, "bedrock")}, packs...)
	o.notePackHostAccess(packs, &packChannel{served: o.servedDaemons(o.jailDaemonsFor(cfg, "podman", withAgent))}, nil)

	// The caller token stays the TOKEN, never the value: the banner is a launch's stderr, teed
	// to launch.log, and the value is a credential scoped to the selecting agents' files.
	const want = "Pack environment this launch:\n" +
		"  aws-auth: SETS an environment variable inside the jail: " +
		"AWS_CONTAINER_AUTHORIZATION_TOKEN={caller_token} " +
		"when the selected provider's platform is \"aws-bedrock\"  [env]\n" +
		"  aws-auth: SETS an environment variable inside the jail: " +
		"AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:1461/credentials " +
		"when the selected provider's platform is \"aws-bedrock\"  [env]\n"
	if got := stderr.String(); got != want {
		t.Errorf("the launch banner's env disclosure for aws-auth:\n--- got ---\n%s--- want ---\n%s",
			got, want)
	}
}

// TestLaunchBannerKeepsAnUngatedEnvVariableBare: the control. An unconditional contribution's
// line has no gate to name and must not grow one, and a pack with both kinds prints each once.
func TestLaunchBannerKeepsAnUngatedEnvVariableBare(t *testing.T) {
	p := &packload.Pack{Name: "widget", Decl: envDisclosureDecl(t, `{"contributes": [
	  {"kind": "env", "vars": {"WIDGET_PLAIN": "1"}},
	  {"kind": "env", "profile": "gate", "vars": {"WIDGET_POINTER": "x"}}
	]}`)}
	var stderr bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &stderr
	o.Stdout = discardBuf()
	o.notePackHostAccess([]*packload.Pack{p}, nil, nil)

	const want = "Pack environment this launch:\n" +
		"  widget: SETS an environment variable inside the jail: WIDGET_PLAIN=1  [env]\n" +
		"  widget: SETS an environment variable inside the jail: WIDGET_POINTER=x " +
		"when profile \"gate\" is active  [env]\n"
	if got := stderr.String(); got != want {
		t.Errorf("the launch banner's env disclosure:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

// envDisclosureDecl decodes a fixture manifest strictly, failing the test on any problem.
func envDisclosureDecl(t *testing.T, body string) *packdecl.Manifest {
	t.Helper()
	m, problems := packdecl.Decode([]byte(body))
	if len(problems) > 0 {
		t.Fatalf("fixture manifest refused: %v", problems)
	}
	return m
}

// bannerOf is the launch banner's env disclosure for la, printed from the channel la's launch
// composed, as both production call sites print it.
func bannerOf(t *testing.T, la assembled) string {
	t.Helper()
	var stderr bytes.Buffer
	la.o.Stderr, la.o.Stdout = &stderr, discardBuf()
	la.o.notePackHostAccess(la.in.packs, la.in.envChannel(la.o), nil)
	return stderr.String()
}

// THE BANNER NAMES THE ADDRESS THE JAIL GETS, not the `{listen}` template (NC-D46). A launch
// disclosure describes what is about to run on this machine, which is why it resolves {state}
// too: on a bridged launch the pointer's line names the declared port, byte-identical to the
// banner before served addresses, and on a shared network namespace it names the port the
// launch picked, the one the adapter's argv binds.
func TestLaunchBannerNamesThePointersServedAddress(t *testing.T) {
	const pointer = "CODEX_REFRESH_TOKEN_URL_OVERRIDE="
	bridged := bannerOf(t, servedPortLaunch(t, nil))
	if !strings.Contains(bridged, pointer+"http://127.0.0.1:1460/oauth/token  [env]") {
		t.Errorf("a bridged launch's banner does not name the declared address:\n%s", bridged)
	}
	la := servedPortLaunch(t, func(o *Options) { o.Network = "host" })
	listen := adapterListen(t, la.argv)
	shared := bannerOf(t, la)
	if !strings.Contains(shared, pointer+"http://"+listen+"/oauth/token  [env]") {
		t.Errorf("a shared-namespace launch's banner does not name the adapter's served address %s:\n%s",
			listen, shared)
	}
	for name, out := range map[string]string{"bridged": bridged, "shared": shared} {
		if strings.Contains(out, "{listen}") {
			t.Errorf("the %s banner prints the unresolved template:\n%s", name, out)
		}
	}
}

// EVERY PRODUCTION CALL HANDS THE BANNER THE LAUNCH'S CHANNEL, whose served set is where the
// pointers were composed. A call passing nil (or anything else) prints `{listen}` for a value
// the jail receives resolved, and no fixture above reaches Run's two call sites.
func TestEveryBannerCallSitePassesTheLaunchChannel(t *testing.T) {
	calls := 0
	for fn, decl := range map[string]*ast.FuncDecl{
		"Run": funcDecl(t, "run.go", "Run"), "runContainer": methodDecl(t, "run.go", "runContainer"),
	} {
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "notePackHostAccess" {
				return true
			}
			calls++
			if len(call.Args) != 3 {
				t.Errorf("%s calls %s with %d arguments", fn, sel.Sel.Name, len(call.Args))
				return true
			}
			if id, ok := call.Args[1].(*ast.Ident); !ok || id.Name != "channel" {
				t.Errorf("%s hands notePackHostAccess something other than the launch's channel", fn)
			}
			// The container arm hands it the user's mise_tools its jail was not handed, for the
			// sealed withheld line (FP-D19); the macos-user arm runs no sealed build.
			want := map[string]string{"Run": "nil", "runContainer": "miseWithheld"}[fn]
			if id, ok := call.Args[2].(*ast.Ident); !ok || id.Name != want {
				t.Errorf("%s hands notePackHostAccess %s as its withheld mise_tools, want %s", fn,
					types.ExprString(call.Args[2]), want)
			}
			return true
		})
	}
	if calls != 2 {
		t.Errorf("found %d notePackHostAccess calls in Run and runContainer, want 2 (the macos-user "+
			"arm and the container arm)", calls)
	}
}

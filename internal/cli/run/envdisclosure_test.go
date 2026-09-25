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
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestLaunchBannerQualifiesAGatedEnvVariable is the golden for the shipped aws-auth pack's
// read disclosure: exactly one env line, carrying its `bedrock` gate. The banner reads the
// footprint (disclosedClaims), so this fails if the footprint's env loop starts claiming a
// gated contribution unconditionally again, or if the banner stops printing env claims.
func TestLaunchBannerQualifiesAGatedEnvVariable(t *testing.T) {
	var stderr bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &stderr
	o.Stdout = discardBuf()
	o.notePackHostAccess([]*packload.Pack{officialPack(t, "aws-auth")})

	const want = "Pack environment this launch:\n" +
		"  aws-auth: SETS an environment variable inside the jail: " +
		"AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:1461/credentials " +
		"when profile \"bedrock\" is active  [env]\n"
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
	o.notePackHostAccess([]*packload.Pack{p})

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

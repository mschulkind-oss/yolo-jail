package run

import (
	"bytes"
	"go/ast"
	"strings"
	"testing"
)

// versionSkewOptions builds an Options whose host version is `host`, via the same
// YOLO_VERSION override version.Get consults first.
func versionSkewOptions(t *testing.T, host string) (*Options, *bytes.Buffer) {
	t.Helper()
	t.Setenv("YOLO_VERSION", host)
	var stderr bytes.Buffer
	return &Options{Stderr: &stderr}, &stderr
}

// TestAttachWarnsWhenTheJailIsOlderThanTheLauncher is the notice itself. It has
// to carry BOTH versions and the remedy: the launch banner already prints the
// baked one, so a line that only repeated it would say nothing new.
func TestAttachWarnsWhenTheJailIsOlderThanTheLauncher(t *testing.T) {
	o, stderr := versionSkewOptions(t, "0.8.0+1160.gd6b2fc82")

	o.warnIfJailIsOlderThanTheLauncher("0.8.0+1114.g6025161d")

	out := stderr.String()
	for _, want := range []string{"0.8.0+1114.g6025161d", "0.8.0+1160.gd6b2fc82", "yolo stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("the skew notice must contain %q:\n%s", want, out)
		}
	}
}

// TestAttachSkewStaysSilent is the half that decides whether this is a useful
// line or one a user learns to scroll past. Two of these are "I cannot tell",
// and a warning that fires on those is noise with no action behind it.
func TestAttachSkewStaysSilent(t *testing.T) {
	cases := []struct {
		name        string
		host, baked string
		why         string
	}{
		{"same version", "0.8.0+1160", "0.8.0+1160", "there is no skew"},
		{"no baked version", "0.8.0+1160", "",
			"a container older than the variable is not evidence of anything"},
		{"unstamped host binary", "unknown", "0.8.0+1114",
			"nothing to compare against — the launcher cannot name itself"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, stderr := versionSkewOptions(t, tc.host)
			o.warnIfJailIsOlderThanTheLauncher(tc.baked)
			if out := stderr.String(); out != "" {
				t.Errorf("must stay silent (%s), got:\n%s", tc.why, out)
			}
		})
	}
}

// TestAttachExistingWarnsAboutSkew is the call-site pin. attachExisting cannot be
// driven to completion here (its exec spawns a real runtime), so without this the
// notice can be dropped with every test above green — and since flake-bundle
// generations, a skewed jail no longer announces itself by breaking.
func TestAttachExistingWarnsAboutSkew(t *testing.T) {
	fn := methodDecl(t, "run.go", "attachExisting")
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok &&
			sel.Sel.Name == "warnIfJailIsOlderThanTheLauncher" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("attachExisting no longer calls warnIfJailIsOlderThanTheLauncher. An old " +
			"jail now keeps working across a host `just install` rather than breaking, so " +
			"nothing else tells the user their session is running an older yolo-entrypoint " +
			"than the launcher that started it. If the call moved, move this pin.")
	}
}

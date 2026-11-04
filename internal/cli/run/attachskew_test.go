package run

import (
	"bytes"
	"go/ast"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// versionSkewOptions builds an Options whose host version is `host`, via the same
// YOLO_VERSION override version.Get consults first.
func versionSkewOptions(t *testing.T, host string) (*Options, *bytes.Buffer) {
	t.Helper()
	t.Setenv("YOLO_VERSION", host)
	var stderr bytes.Buffer
	return &Options{Stderr: &stderr}, &stderr
}

func TestAttachCustomSetupRefusalOffersBackendBuiltinWithoutChangingConfig(t *testing.T) {
	for _, tc := range []struct {
		runtime, builtin, backend, inspect string
	}{
		{"podman", "podman-self", "Podman", setup.EnvVar + "=lab\n"},
		{"container", "apple-container-self", "Apple Container", `[{"configuration":{"initProcess":{"environment":["` + setup.EnvVar + `=lab"]}}}]`},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			var stderr bytes.Buffer
			o := &Options{
				Stderr: &stderr,
				Now:    time.Now,
				Setup:  setup.Setup{Name: "lab"},
				Exec: func([]string, string, []string, time.Duration) ExecResult {
					return ExecResult{Ran: true, RC: 0, Stdout: tc.inspect}
				},
			}
			rc, restarted := o.attachExisting("yolo-ws-abcd1234", tc.runtime, "claude", nil,
				stagedPacks{}, nil, false, nil)
			if rc != 1 || restarted {
				t.Fatalf("custom setup re-entry result = (%d, %t), want a refusal and no restart", rc, restarted)
			}
			got := stderr.String()
			for _, want := range []string{
				"Refusing to attach", "custom setup re-entry is unsupported", "yolo stop",
				"--setup " + tc.builtin, tc.backend, "saved setup", "unchanged",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("custom setup refusal lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestRestartActionRemedyMatchesBackend(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, command, remedy string
	}{
		{"podman", "podman", "yolo stop", "'yolo stop' from this workspace"},
		{"container", "container", "yolo stop", "'yolo stop' from this workspace"},
		{"macos-user", "macos-user", "yolo", "end this session, then run `yolo` from this workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			action := restartActionFor(tc.runtime, "yolo-ws-abcd1234")
			if action.command != tc.command || action.remedy != tc.remedy {
				t.Fatalf("restart action = %#v, want command %q and remedy %q", action, tc.command, tc.remedy)
			}
			if got := stopCommand(tc.runtime, "yolo-ws-abcd1234"); got != action.command {
				t.Errorf("stopCommand = %q, want restart action command %q", got, action.command)
			}
			if got := stopRemedy(tc.runtime, "yolo-ws-abcd1234"); got != action.remedy {
				t.Errorf("stopRemedy = %q, want restart action remedy %q", got, action.remedy)
			}
		})
	}
}

func TestKeyedStopRemedyIsDistinctFromSessionRestart(t *testing.T) {
	const key = "yolo-ws-abcd1234"
	if got, want := stopRemedy("macos-user", key), "end this session, then run `yolo` from this workspace"; got != want {
		t.Fatalf("ordinary native restart = %q, want %q", got, want)
	}
	if got, want := keyedStopRemedy("macos-user", key), "'yolo stop' from this workspace"; got != want {
		t.Fatalf("keyed native stop = %q, want %q", got, want)
	}
	if got, want := keyedStopRemedy("podman", key), stopRemedy("podman", key); got != want {
		t.Fatalf("container keyed stop = %q, want ordinary stop remedy %q", got, want)
	}
	if got, want := mechanicsRestartRemedy("macos-user", key), "end this session, then run `yolo` from this workspace again"; got != want {
		t.Fatalf("native mechanics remedy = %q, want %q", got, want)
	}
	if got, want := mechanicsRestartRemedy("podman", key), "restart this jail with 'yolo stop' from this workspace, then run `yolo` again"; got != want {
		t.Fatalf("container mechanics remedy = %q, want %q", got, want)
	}
}

// The refusal must use the same backend-aware remedy pinned above; otherwise
// deleting its production callsite would leave only a callee test green.
func TestAttachSkewRefusalUsesRestartRemedy(t *testing.T) {
	fn := methodDecl(t, "contracttags.go", "refuseAttachSkew")
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.Ident); ok && sel.Name == "stopRemedy" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("refuseAttachSkew no longer uses stopRemedy; the refusal must give the backend's real restart action")
	}
}

// TestAttachWarnsWhenTheJailIsOlderThanTheLauncher is the notice itself. It has
// to carry BOTH versions and the remedy: the launch banner already prints the
// baked one, so a line that only repeated it would say nothing new.
func TestAttachWarnsWhenTheJailIsOlderThanTheLauncher(t *testing.T) {
	o, stderr := versionSkewOptions(t, "0.8.0+1160.gd6b2fc82")

	o.warnIfJailIsOlderThanTheLauncher("podman", "yolo-ws-abcd1234", "0.8.0+1114.g6025161d")

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
			o.warnIfJailIsOlderThanTheLauncher("podman", "yolo-ws-abcd1234", tc.baked)
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

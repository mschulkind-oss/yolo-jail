package cli

// hostenvoverride_test.go pins notch-convergence item 13 at the host (rows A7 and A8): the
// OQ-SSO8 override check, the provider and profile section of validation, and the profile
// disclosure line, each through the jail's own function. None of them ran at `yolo host`, so the
// host ran claude with a bearer beside the pointer it silently beats, which every jail launch
// refuses (MEASURED 2026-09-27), composed providers written with removed keys, and never said
// where its profile landed.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// writeHostWidgetPack writes the conventional local pack with an env contribution gated on
// `bedrock` and an `overridden_by` entry of its own: a pointer that points at no yolo daemon, so
// the host delivers it (it declares no `served_by`). certain=false makes the override a warning.
func writeHostWidgetPack(t *testing.T, home string, certain bool) {
	t.Helper()
	certainty := ""
	if !certain {
		certainty = `, "certain": false`
	}
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"),
		`{"name": "local", "contributes": [{"kind": "env", "profile": "bedrock",
	  "vars": {"WIDGET_POINTER": "https://widget.example/creds"},
	  "overridden_by": [{"vars": ["WIDGET_TOKEN"], "because": "the widget client reads WIDGET_TOKEN first"`+
			certainty+`}]}]}`)
}

// widgetHome is a host home selecting claude, with WIDGET_TOKEN in env_sources when inSources.
func widgetHome(t *testing.T, inSources, certain bool) string {
	t.Helper()
	cfg := `{"packs": ["claude"]}`
	if inSources {
		cfg = `{"packs": ["claude"], "env_sources": [{"WIDGET_TOKEN": "frozen"}]}`
	}
	home := hostGateHome(t, cfg, nil)
	t.Setenv("WIDGET_TOKEN", "")
	writeHostWidgetPack(t, home, certain)
	return home
}

// THE OVERRIDE CHECK REFUSES `yolo host -- claude` as it refuses a jail launch: the pointer and
// the variable its pack says beats it, from either delivery. The inherited shell counts here and
// not in a jail, because the agent `yolo host` execs inherits it (the one named input that
// differs). The refusal is packload's own text, the one every jail arm prints.
func TestHostLaunchRefusesAPointerBesideItsOverride(t *testing.T) {
	for _, tc := range []struct {
		name, origin string
		inSources    bool
		shell        string
	}{
		{"env_sources", packload.FromEnvSources, true, ""},
		{"the invoking shell", packload.FromLaunchEnv, false, "frozen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := widgetHome(t, tc.inSources, true)
			t.Setenv("WIDGET_TOKEN", tc.shell)
			rc, reached, errw := hostExecRun(t, "claude", "-p", "bedrock")
			if rc != 1 || reached {
				t.Fatalf("yolo host -p bedrock -- claude with WIDGET_TOKEN from %s must refuse "+
					"before the exec: rc=%d reached=%v\n%s", tc.name, rc, reached, errw)
			}
			for _, want := range []string{"WIDGET_TOKEN is delivered by " + tc.origin,
				"WIDGET_POINTER", "pack local", "Drop one."} {
				if !strings.Contains(errw, want) {
					t.Errorf("the refusal must say %q:\n%s", want, errw)
				}
			}
			// The control: the same home without the overriding variable launches.
			t.Setenv("WIDGET_TOKEN", "")
			userCfg(t, home, `{"packs": ["claude"]}`)
			if rc, reached, errw := hostExecRun(t, "claude", "-p", "bedrock"); rc != 0 || !reached {
				t.Fatalf("fixture control: without WIDGET_TOKEN the launch runs: rc=%d\n%s", rc, errw)
			}
		})
	}
}

// An UNCERTAIN override warns and the launch goes on, as in a jail; `yolo host env` discloses a
// certain one without refusing, as it discloses the credential pre-flight's answer.
func TestHostOverrideWarningsAndTheObserveVerb(t *testing.T) {
	widgetHome(t, true, false)
	rc, reached, errw := hostExecRun(t, "claude", "-p", "bedrock")
	if rc != 0 || !reached || !strings.Contains(errw, "WIDGET_TOKEN is delivered by") {
		t.Errorf("an uncertain override warns and launches: rc=%d reached=%v\n%s", rc, reached, errw)
	}

	widgetHome(t, true, true)
	var out, envErr bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude", "-p", "bedrock"}, &out, &envErr, false, nil); rc != 0 {
		t.Fatalf("yolo host env is an observe verb and answers: rc=%d\n%s", rc, envErr.String())
	}
	if !strings.Contains(envErr.String(), "WIDGET_TOKEN is delivered by "+packload.FromEnvSources) {
		t.Errorf("yolo host env must disclose the override:\n%s", envErr.String())
	}
}

// THE MEASURED LAUNCH, since served-address composition: `["claude"]` on bedrock with a bearer
// beside aws-auth's pointer. The pointer names the daemon that serves it and is withheld where
// that daemon does not run (NC-D16), and a withheld contribution has nothing to override, so the
// launch runs, names the pointer it withheld, and is not refused over the bearer: the answer a
// jail gives wherever aws-auth is not served (macos-user,
// TestMacosUserWithholdsTheBedrockPointerSoNothingOverridesIt in internal/cli/run).
func TestHostBearerBesideAWithheldPointerOverridesNothing(t *testing.T) {
	hostGateHome(t, `{"packs": ["claude"], "env_sources": [{"AWS_BEARER_TOKEN_BEDROCK": "sk-frozen"}]}`, nil)
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")
	rc, reached, errw := hostExecRun(t, "claude", "-p", "bedrock")
	if rc != 0 || !reached {
		t.Fatalf("rc=%d reached=%v\n%s", rc, reached, errw)
	}
	if strings.Contains(errw, "Drop one.") || !strings.Contains(errw, "AWS_CONTAINER_CREDENTIALS_FULL_URI") {
		t.Errorf("the withheld pointer is named and nothing refuses over it:\n%s", errw)
	}
}

// THE PROFILE DISCLOSURE LINE, the jail's (packload.ProfileDisclosures), at both host front doors.
func TestHostLaunchSaysWhereItsProfileLanded(t *testing.T) {
	const want = "Profile bedrock: declared: claude; received: aws-auth, claude, openai-auth, wire-bridge"
	_, errs := hostGateLaunchWith(t, claudeAlone, nil, []string{"-p", "bedrock"}, "claude")
	if !strings.Contains(errs, "yolo host: "+want) {
		t.Errorf("yolo host -p bedrock -- claude must print %q:\n%s", want, errs)
	}
	hostGateHome(t, claudeAlone, nil)
	var out, envErr bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude", "-p", "bedrock"}, &out, &envErr, false, nil); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, envErr.String())
	}
	if !strings.Contains(envErr.String(), "yolo host env: "+want) {
		t.Errorf("yolo host env must print %q:\n%s", want, envErr.String())
	}
	// No profile, no line.
	_, errs = hostGateLaunchWith(t, claudeAlone, nil, nil, "claude")
	if strings.Contains(errs, "Profile ") {
		t.Errorf("a launch with no profile prints no profile line:\n%s", errs)
	}
}

// THE PROFILE SECTION OF VALIDATION: a malformed `profiles` entry refuses the host launch in the
// validator's words, as it refuses every jail launch; the removed `base_url` shorthand is
// TestHostExecRefusesAManufacturedAddressPair's.
func TestHostLaunchRefusesAProfileEntryValidationRefuses(t *testing.T) {
	rc, env, errs := hostGateRun(t, `{"packs": ["claude"], "profiles": {"mine": {"provider": 7}}}`, nil, nil, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("a malformed profiles entry must refuse the host launch: rc=%d\n%s", rc, errs)
	}
	for _, want := range []string{"config.profiles.mine", "`yolo check` reports the same"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
}

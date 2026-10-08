package run

import (
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// EVERY STARTUP REFUSAL ENDS WITH THE RETRY, and names `yolo check --no-build` exactly once,
// whether or not the daemon's own remedy already named it.
func TestHostStartupRefusalAlwaysEndsWithTheRetry(t *testing.T) {
	for _, remedy := range []string{
		"",
		"Install the AWS CLI v2 on the host.",
		"Correct the settings and run `yolo check --no-build` again.",
	} {
		text := (&hostStartupRefusal{name: "svc", class: "configuration", reason: "refused", remedy: remedy}).Error()
		if !strings.HasSuffix(strings.TrimSpace(text), "retry the launch.") {
			t.Errorf("remedy %q: refusal does not end with the retry:\n%s", remedy, text)
		}
		if n := strings.Count(text, "yolo check --no-build"); n != 1 {
			t.Errorf("remedy %q: refusal names `yolo check --no-build` %d times, want 1:\n%s", remedy, n, text)
		}
	}
}

func TestLoopholeSettingsFixStepNamesTheKeyItsFileAndThePreflight(t *testing.T) {
	// A user-scope setting is refused in the workspace file, so its step names the user config alone.
	user := &loopholes.Loophole{Name: "aws-auth", Settings: []loopholes.Setting{
		{Key: "profile", Scope: loopholes.SettingScopeUser}}}
	step := LoopholeSettingsFixStep(user)
	for _, want := range []string{"`loopholes.aws-auth.settings`", paths.UserConfigPath(), "`yolo check --no-build`"} {
		if !strings.Contains(step, want) {
			t.Errorf("settings fix step lacks %q: %s", want, step)
		}
	}
	if strings.Contains(step, "yolo-jail.jsonc") {
		t.Errorf("a user-scope setting's fix step names the workspace file, where it is refused: %s", step)
	}
	workspace := &loopholes.Loophole{Name: "svc", Settings: []loopholes.Setting{
		{Key: "mode", Scope: loopholes.SettingScopeWorkspace}}}
	if step := LoopholeSettingsFixStep(workspace); !strings.Contains(step, "yolo-jail.jsonc") {
		t.Errorf("a workspace-scope setting's fix step does not name the workspace file: %s", step)
	}
}

// A refusal that is not about the settings (a validator timeout) never tells the user to correct
// them, and still ends with the retry.
func TestANonConfigurationStartupRefusalDoesNotBlameTheSettings(t *testing.T) {
	text := (&hostStartupRefusal{name: "svc", class: "timeout", reason: "The settings validator did not finish.",
		remedy: "Retry the launch; if it times out again, report it."}).Error()
	if strings.Contains(text, "Correct the settings") {
		t.Errorf("a timeout refusal blames the settings:\n%s", text)
	}
	if !strings.HasSuffix(strings.TrimSpace(text), "retry the launch.") {
		t.Errorf("a timeout refusal does not end with the retry:\n%s", text)
	}
}

func TestStartupDiagnosticsFailureNamesTheNextStep(t *testing.T) {
	line := startupDiagnosticsFailure("aws-auth", errors.New("socketpair: too many open files"))
	for _, want := range []string{"aws-auth", "too many open files", "Retry the launch", "`ulimit -n`"} {
		if !strings.Contains(line, want) {
			t.Errorf("startup diagnostics failure lacks %q: %s", want, line)
		}
	}
}

func TestLaunchCheckTextReplacesFormatCharacters(t *testing.T) {
	got := launchCheckText("ok\u202eevil\u200bhidden")
	for _, r := range got {
		if unicode.Is(unicode.Cf, r) {
			t.Fatalf("launchCheckText kept format character %U: %q", r, got)
		}
	}
	if got != "ok evil hidden" {
		t.Fatalf("launchCheckText = %q, want format characters replaced by spaces", got)
	}
}

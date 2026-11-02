package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// The owner's shape: a TOML user config whose config.d include selects Bedrock and declares a
// model with no price table. A launch accepts it (docs/design/readable-config.md RC-D29 (6)).
const pricelessBedrockFragment = "[allow.github]\nalways = [\"read\"]\non_request = [\"approve_runs\"]\n\n" +
	"[active_provider]\ndefault = \"bedrock\"\n\n" +
	"[providers.bedrock.models.\"global.anthropic.claude-haiku-5-5\"]\nname = \"Haiku 5.5\"\ncontext = 1000000\n"

// pricelessModelLocation is where the fragment declares the model: its table header.
const pricelessModelLocation = "config.d/work.toml:8:1"

func pricelessModelFixture(t *testing.T) namedVerbFixture {
	t.Helper()
	ws := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs = [\"git\", \"github\", \"claude\", \"bedrock\"]\n")
	fragmentPath := filepath.Join(filepath.Dir(user.Path), "config.d", "work.toml")
	if err := os.MkdirAll(filepath.Dir(fragmentPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragmentPath, []byte(pricelessBedrockFragment), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	withConfigEditCWD(t, ws.Root)
	return namedVerbFixture{workspace: ws.Root, userConfig: user.Path, fragment: fragmentPath, project: ws.Path}
}

// A config verb is not refused for a problem it did not introduce: the request is written, and
// the priceless model is one warning naming where it is declared and the fix. The launch's
// validation of the same config agrees that it is valid.
func TestRequestIsNotRefusedForAPricelessModelItDidNotWrite(t *testing.T) {
	fx := pricelessModelFixture(t)
	fragmentBefore := readFile(t, fx.fragment)

	rc, out, errs := captureStd(t, func() int {
		return registry["request"]([]string{"request", "github", "approve_runs", "github-actions[bot]"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	if strings.Contains(errs, "config edit refused") {
		t.Fatalf("the request was refused for a model it did not write: %q", errs)
	}
	if got := readFile(t, fx.project); !strings.Contains(got, `approve_runs = ["github-actions[bot]"]`) {
		t.Fatalf("project file lacks the request:\n%s\nstdout=%q stderr=%q", got, out, errs)
	}
	if strings.Count(errs, "warning: already in your config") != 1 {
		t.Fatalf("want exactly one pre-existing-problem warning: %q", errs)
	}
	for _, want := range []string{pricelessModelLocation, `model "global.anthropic.claude-haiku-5-5"`,
		"no authored price facts", "add price with input, output, cache_read and cache_write", "a launch runs without it"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("the warning lacks %q: %q", want, errs)
		}
	}
	if readFile(t, fx.fragment) != fragmentBefore {
		t.Fatal("the request edited the file that declares the model")
	}

	// The launch's verdict: the post-write check, and the validation a launch runs, accept the
	// same config the verb just accepted.
	if !strings.Contains(out, "[PASS] Merged config is semantically valid") {
		t.Fatalf("the post-write check did not accept the merged config: %q", out)
	}
	cfg, err := config.LoadReadableConfig(fx.workspace, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	launchErrs, _ := config.ValidateConfig(cfg, fx.workspace, nil)
	for _, problem := range launchErrs {
		if strings.Contains(problem, "price") || strings.Contains(problem, "claude-haiku-5-5") {
			t.Fatalf("the launch's validation refuses the priceless model, so the verb and the launch disagree: %v", launchErrs)
		}
	}
}

// An edit that itself writes a priceless model is still refused (RC-D29 (6)), naming the model
// it wrote, not the one already there; nothing is written.
func TestConfigSetIsRefusedForAPricelessModelItWrites(t *testing.T) {
	fx := pricelessModelFixture(t)
	userBefore, fragmentBefore := readFile(t, fx.userConfig), readFile(t, fx.fragment)
	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "set", "providers.bedrock.models.claude-sonnet-9.name", "Sonnet 9", "--user"})
	})
	if rc == 0 || !strings.Contains(errs, "config edit refused") {
		t.Fatalf("an edit writing a priceless model was not refused: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	for _, want := range []string{`model "claude-sonnet-9"`, "no authored price facts"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("the refusal lacks %q: %q", want, errs)
		}
	}
	if strings.Contains(errs, "claude-haiku-5-5") {
		t.Fatalf("the refusal blames the model the edit did not write: %q", errs)
	}
	if readFile(t, fx.userConfig) != userBefore || readFile(t, fx.fragment) != fragmentBefore {
		t.Fatal("a refused edit wrote the user config")
	}
}

// An edit that introduces an unrelated invalid value is still refused over a config that already
// has a problem: only the pre-existing problem is tolerated.
func TestConfigSetIsRefusedForANewInvalidValueOverAPreexistingProblem(t *testing.T) {
	fx := pricelessModelFixture(t)
	userBefore := readFile(t, fx.userConfig)
	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "set", "network.mode", "bogus", "--user"})
	})
	if rc == 0 || !strings.Contains(errs, "config edit refused") || !strings.Contains(errs, "config.network.mode: expected 'bridge' or 'host'") ||
		strings.Contains(errs, "claude-haiku-5-5") {
		t.Fatalf("an edit writing an invalid network.mode was not refused: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if readFile(t, fx.userConfig) != userBefore {
		t.Fatal("a refused edit wrote the user config")
	}
}

// A verb that creates the file it writes is not refused for a problem the rest of the config
// already had either: the new file's own published-bytes check tolerates it as the plan did.
func TestRequestCreatingTheProjectFileIsNotRefusedForAPricelessModel(t *testing.T) {
	fx := pricelessModelFixture(t)
	if err := os.Remove(fx.project); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := captureStd(t, func() int {
		return registry["request"]([]string{"request", "github", "approve_runs", "github-actions[bot]"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	if strings.Contains(errs, "config edit refused") || !strings.Contains(out, "[PASS] Merged config is semantically valid") {
		t.Fatalf("creating the project file was refused for a model it did not write: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, fx.project); !strings.Contains(got, `approve_runs = ["github-actions[bot]"]`) {
		t.Fatalf("project file lacks the request:\n%s", got)
	}
	if strings.Count(errs, "warning: already in your config") != 1 || !strings.Contains(errs, pricelessModelLocation) {
		t.Fatalf("want one pre-existing-problem warning naming %s: %q", pricelessModelLocation, errs)
	}
}

// Unsetting an unrelated scalar validates the original config first; a problem the removal
// leaves as it was does not refuse it.
func TestUnsetScalarIsNotRefusedForAPricelessModelItLeaves(t *testing.T) {
	fx := pricelessModelFixture(t)
	if err := os.WriteFile(fx.project, []byte("[machine.resources]\ncpus = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "unset", "machine.resources.cpus", "--project"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	if strings.Contains(errs, "config edit refused") || !strings.Contains(out, "[PASS] Merged config is semantically valid") {
		t.Fatalf("an unrelated unset was refused for a pre-existing model: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if strings.Contains(readFile(t, fx.project), "cpus") {
		t.Fatal("the unset did not remove the value")
	}
	if strings.Count(errs, "warning: already in your config") != 1 {
		t.Fatalf("want exactly one pre-existing-problem warning: %q", errs)
	}
}

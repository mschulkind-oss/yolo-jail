package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// secret-env-scoping.md §3.7: `yolo check` predicts the launch's row 4 line from the same gate, as
// a WARN (secret-env-scoping-plan.md SEP-D9), naming the variable and never its value.
func TestSecretEnvGapWarnsOfAPrivateNameNothingClaims(t *testing.T) {
	set := jsonx.NewOrderedMap()
	set.Set("STRIPE_KEY", "s-probe")
	secrets := jsonx.NewOrderedMap()
	secrets.Set("set", set)
	env := jsonx.NewOrderedMap()
	env.Set("secrets", secrets)
	merged := jsonx.NewOrderedMap()
	merged.Set("env", env)
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.toml"), []byte("[env.secrets]\nset = { STRIPE_KEY = \"s-probe\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope := overrideScope(nil, merged, packload.ServedDaemons{}, ws, func(string) {},
		func() (map[string]packload.UserProfile, error) { return nil, nil })
	fails, warns := secretEnvGap(scope, config.ResolveEnv(ws, merged, func(string) {}), ws)
	if len(fails) != 0 || len(warns) != 1 || !strings.Contains(warns[0].msg, "STRIPE_KEY: withheld from every process") {
		t.Fatalf("one warning naming the withheld private name, got %+v", warns)
	}
	// SEP-D6: the verb names the scope whose config sets the name.
	if !strings.Contains(warns[0].msg, "yolo config unsecret STRIPE_KEY --project") {
		t.Errorf("the warning's verb names the project scope: %+v", warns[0])
	}
	if strings.Contains(warns[0].msg+warns[0].note, "s-probe") {
		t.Errorf("a warning never carries a value: %+v", warns[0])
	}
}

// The Packs section prints that warning: the call site, not only the helper.
func TestThePacksSectionWarnsOfAPrivateNameNothingClaims(t *testing.T) {
	packsFixture(t, `{"packs": []}`)
	set := jsonx.NewOrderedMap()
	set.Set("STRIPE_KEY", "s-probe")
	secrets := jsonx.NewOrderedMap()
	secrets.Set("set", set)
	env := jsonx.NewOrderedMap()
	env.Set("secrets", secrets)
	merged := jsonx.NewOrderedMap()
	merged.Set("env", env)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).sectionPacks(r, merged)
	out := buf.String()
	if !strings.Contains(out, "STRIPE_KEY: withheld from every process") || strings.Contains(out, "s-probe") {
		t.Errorf("the Packs section names the withheld private name, never its value:\n%s", out)
	}
}

// secret-env-scoping-plan.md Task 7: both refusals are a FAIL in check, in the launch's words, through
// the Packs section's call site. A name in both sets refuses whatever claims it (SE-D2).
func TestThePacksSectionFailsANameInBothSets(t *testing.T) {
	packsFixture(t, `{"packs": []}`)
	pub := jsonx.NewOrderedMap()
	pub.Set("FOO", "a-probe")
	prv := jsonx.NewOrderedMap()
	prv.Set("FOO", "b-probe")
	secrets := jsonx.NewOrderedMap()
	secrets.Set("set", prv)
	env := jsonx.NewOrderedMap()
	env.Set("set", pub)
	env.Set("secrets", secrets)
	merged := jsonx.NewOrderedMap()
	merged.Set("env", env)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).sectionPacks(r, merged)
	out := buf.String()
	if r.failed == 0 || !strings.Contains(out, "config.env: FOO is set in both your public environment ([env] set)") ||
		!strings.Contains(out, "yolo config secret FOO --drop") || !strings.Contains(out, "yolo config unsecret FOO --drop") {
		t.Errorf("a FAIL naming both --drop commands:\n%s", out)
	}
	if strings.Contains(out, "-probe") {
		t.Errorf("a refusal never carries a value:\n%s", out)
	}
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func omap(kv ...any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

func writeEnv(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveEnvKeepsWhichSetDeclaredEachName(t *testing.T) {
	dir := t.TempDir()
	pub, priv := filepath.Join(dir, "defaults.env"), filepath.Join(dir, "secrets.env")
	writeEnv(t, pub, "GH_TOKEN=g\nDEBUG=1\n")
	writeEnv(t, priv, "ZAI_API_KEY=z\n")
	cfg := omap("env", omap(
		"files", []any{pub},
		"set", omap("EDITOR_THEME", "dark"),
		"secrets", omap("files", []any{priv}, "set", omap("OPENROUTER_API_KEY", "sk-or")),
	))
	h := ResolveEnv(dir, cfg, nil)
	if got := strings.Join(h.Values().Keys(), ","); got != "GH_TOKEN,DEBUG,EDITOR_THEME,ZAI_API_KEY,OPENROUTER_API_KEY" {
		t.Errorf("values in hydration order = %s", got)
	}
	for name, want := range map[string]bool{"GH_TOKEN": false, "DEBUG": false, "EDITOR_THEME": false,
		"ZAI_API_KEY": true, "OPENROUTER_API_KEY": true} {
		if h.IsPrivate(name) != want {
			t.Errorf("IsPrivate(%s) = %v, want %v", name, !want, want)
		}
	}
	if p := h.Places("ZAI_API_KEY"); len(p) != 1 || !p[0].Private || p[0].Entry != priv || p[0].Key != "env.secrets.files" {
		t.Errorf("ZAI_API_KEY's place = %+v, want the private file", p)
	}
	if p := h.Places("EDITOR_THEME"); len(p) != 1 || p[0].Private || p[0].Key != "env.set" {
		t.Errorf("EDITOR_THEME's place = %+v, want [env] set", p)
	}
	if len(h.BothSets()) != 0 {
		t.Errorf("no name is in both sets here: %v", h.BothSets())
	}
}

func TestOneUnsetRemovesANameFromBothSets(t *testing.T) {
	cfg := omap("env", omap(
		"unset", []any{"AWS_PROFILE"},
		"secrets", omap("set", omap("AWS_PROFILE", "dev", "KEEP", "k")),
	))
	h := ResolveEnv(t.TempDir(), cfg, nil)
	if _, ok := h.Values().Get("AWS_PROFILE"); ok {
		t.Error("[env] unset must remove a private value of the name too (SE-D4)")
	}
	if got := strings.Join(h.Removals(), ","); got != "AWS_PROFILE" {
		t.Errorf("removals = %s, want AWS_PROFILE", got)
	}
	if v, _ := h.Values().Get("KEEP"); v != "k" {
		t.Errorf("an unrelated private value survives, got %v", v)
	}
}

func TestANameInBothSetsIsReportedWithBothPlaces(t *testing.T) {
	dir := t.TempDir()
	pub, priv := filepath.Join(dir, "defaults.env"), filepath.Join(dir, "secrets.env")
	writeEnv(t, pub, "ZAI_API_KEY=old\n")
	writeEnv(t, priv, "ZAI_API_KEY=new\n")
	cfg := omap("env", omap("files", []any{pub}, "secrets", omap("files", []any{priv})))
	h := ResolveEnv(dir, cfg, nil)
	if got := strings.Join(h.BothSets(), ","); got != "ZAI_API_KEY" {
		t.Fatalf("BothSets = %s, want ZAI_API_KEY", got)
	}
	places := h.Places("ZAI_API_KEY")
	if len(places) != 2 || places[0].Private || !places[1].Private {
		t.Errorf("places = %+v, want the public place then the private one", places)
	}
	if v, _ := h.Values().Get("ZAI_API_KEY"); v != "new" {
		t.Errorf("a both-sets name holds the private copy (SEP-D1), got %v", v)
	}
}

func TestDescribeEnvSourcesNamesThePrivateEntriesConsulted(t *testing.T) {
	cfg := omap("env", omap("secrets", omap("files", []any{"/x/secrets.env"})))
	if got := strings.Join(DescribeEnvSources("/w", cfg), "|"); !strings.Contains(got, "/x/secrets.env") {
		t.Errorf("the pre-flight's consulted list must name a private file: %s", got)
	}
}

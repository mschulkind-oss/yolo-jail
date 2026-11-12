package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// A legacy grant whose repo is a pattern has no named table either (GW-D80): the JSONC file
// holding it stays whole, unconverted and still read, with the reason and the next step, and no
// TOML companion is written for the rest of it.
func TestMigrateKeepsAJSONCFileWithAPatternGrantWhole(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	jsonc := `{"brokered": {"github": {"grants": [{"kind": "dispatch-workflow", "repo": "github.com/acme/*", "workflow": "nightly.yml"}]}}}` + "\n"
	jsoncPath := filepath.Join(ws, "yolo-jail.jsonc")
	write(t, jsoncPath, jsonc)
	assertRetainedWhole(t, plan(t, Request{Workspace: ws}), jsoncPath, jsonc, "brokered.github.grants")
}

// A host-wide grant, repo "*", is the same case: no named table can carry it, so the whole file
// stays as written (GW-D80), even when another grant in it converts.
func TestMigrateKeepsAJSONCFileWithAHostWildcardGrantWhole(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	jsonc := `{"brokered": {"github": {"grants": [` +
		`{"kind": "dispatch-workflow", "repo": "github.com/acme/app", "workflow": "ci.yml"},` +
		`{"kind": "dispatch-workflow", "repo": "*", "workflow": "nightly.yml"}]}}}` + "\n"
	jsoncPath := filepath.Join(ws, "yolo-jail.jsonc")
	write(t, jsoncPath, jsonc)
	assertRetainedWhole(t, plan(t, Request{Workspace: ws}), jsoncPath, jsonc, "brokered.github.grants")
}

// assertRetainedWhole is the retained-file contract: exactly one not-converted line names the file
// and the key, its reason gives the `yolo request` next step, no target writes the file, no TOML
// companion holds the old key, and the bytes on disk are unchanged.
func assertRetainedWhole(t *testing.T, p *MigrationPlan, path, original, key string) {
	t.Helper()
	if len(p.NotConverted) != 1 || p.NotConverted[0].File != path || p.NotConverted[0].Path != key ||
		!strings.Contains(p.NotConverted[0].Reason, "yolo request") {
		t.Fatalf("not converted = %+v", p.NotConverted)
	}
	for _, tg := range p.Targets {
		if tg.Old == path {
			t.Fatalf("the retained file was converted:\n%s", tg.Bytes)
		}
	}
	if text := targetText(p); strings.Contains(text, "grants") || strings.Contains(text, "repos") {
		t.Fatalf("a TOML companion was written for the retained file:\n%s", text)
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Fatalf("planning changed the file:\n%s", got)
	}
}

// The old reach is projected per source: a source named in legacy is projected as the old file
// reached it, the unqualified lines on every remote, and every other declared source as the new
// file does, the unqualified lines on origin alone. Only github declares origin narrowing, so a
// copy of its declaration stands for a second narrowing source.
func TestTheBeforeSideProjectsOnlyTheSourcesAFileRewrites(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git", "https://github.com/elsewhere/app.git")
	declarations, err := shippedBrokers()
	if err != nil {
		t.Fatal(err)
	}
	var github config.ReadableBrokerDeclaration
	for _, d := range declarations {
		if d.Brokered.Source == "github" {
			github = d
		}
	}
	if github.Brokered.UnqualifiedRequests != "origin" {
		t.Fatalf("github no longer narrows its unqualified lines; this test needs another narrowing source")
	}
	other := github
	other.Brokered.Source = "other"
	ws2 := mustJSON(t, `{"requests": {"github": {"runs": ["ci.yml"]}, "other": {"runs": ["ci.yml"]}}}`)
	grants := mustJSON(t, "{}")
	addWorkspaceBrokerGrantsBySource(grants, ws2, ws, []config.ReadableBrokerDeclaration{github, other}, map[string]bool{"github": true})

	runs := func(source string) []string {
		v, _ := grants.Get(source)
		entry, _ := v.(*jsonx.OrderedMap)
		list, _ := entry.Get("workspace_grants")
		items, _ := list.([]any)
		var out []string
		for _, item := range items {
			out = append(out, item.(string))
		}
		return out
	}
	if got := strings.Join(runs("github"), "; "); !strings.Contains(got, "runs github.com/elsewhere/app ci.yml") {
		t.Fatalf("the named source lost its old reach on the second remote: %s", got)
	}
	if got := strings.Join(runs("other"), "; "); strings.Contains(got, "elsewhere") || !strings.Contains(got, "runs github.com/acme/one ci.yml") {
		t.Fatalf("an unnamed source projected something other than origin's lines: %s", got)
	}
}

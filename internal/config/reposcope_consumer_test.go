package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// reposcope_consumer_test.go pins the consumers of a `[repos.<name>]` table that are not the
// gate's producer (docs/design/github-repository-binding-plan.md, Task 2): the composed-config
// census, the raw-scope roots of an edit's semantic check, the outside-writer refusal, and the
// skip report, which names the scoped allow of a request under a name.

const namedSkipRequests = "[repos.infra]\nrepo = \"github.com/acme/infra\"\n\n" +
	"[repos.infra.requests.github]\nruns = [\"ci.yml\"]\n"

// The composed config a launch validates accepts a workspace's repos table: withReadableScopeKeys
// is where the census says so, and deleting that entry turns this into an unknown-key error.
func TestTheComposedConfigAcceptsAReposTable(t *testing.T) {
	ws := skippedBrokerFixture(t, "[allow.github]\nalways = [\"read\"]\n", namedSkipRequests)
	merged, err := LoadConfig(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(merged, ws, nil)
	for _, e := range errs {
		if strings.Contains(e, "repos") {
			t.Fatalf("a composed config refused its repos table: %q", errs)
		}
	}
}

// The edit path's raw-scope check reaches a user file's named request: `repos.<name>.requests`
// is workspace-only, so a user config naming one is refused with the key it wrote.
func TestExplicitRawScopeRefusesAUserNamedRequest(t *testing.T) {
	ctx := &editValidationContext{
		rawUser:      decode(t, `{"repos":{"mine":{"repo":"github.com/acme/tools","requests":{"github":{"runs":["ci.yml"]}}}}}`),
		rawWorkspace: decode(t, `{}`),
	}
	err := validateExplicitRawReadableScopes(ctx)
	if err == nil || !strings.Contains(err.Error(), "config.repos.mine.requests") {
		t.Fatalf("a user config's named request was not refused by its key: %v", err)
	}
}

// A request under a name above the user's ceiling is named with the scoped allow, which puts it in
// the ceiling for that repository alone, not the broad one (GW-D76).
func TestANamedRequestAboveTheCeilingNamesTheScopedAllow(t *testing.T) {
	ws := skippedBrokerFixture(t, "[allow.github]\nalways = [\"read\"]\n", namedSkipRequests)
	merged, err := LoadConfig(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := ValidateConfig(merged, ws, nil)
	if len(errs) != 0 {
		t.Fatalf("a skipped named request is not an error: %q", errs)
	}
	want := "this project requests repos.infra.github runs ci.yml.\n" +
		"  Your config does not allow runs on request, so this project runs without it.\n" +
		"  To allow it on this repository, run on the host:\n" +
		"    yolo allow repos.infra.github on_request runs\n" +
		"    yolo allow repos infra github.com/acme/infra\n" +
		"  then approve it for this workspace, if that command did not already ask:\n" +
		"    yolo config apply"
	var named []string
	for _, w := range warns {
		if strings.Contains(w, "repos.infra.github") {
			named = append(named, strings.TrimPrefix(w, paths.WorkspaceConfigTOMLName+":"))
		}
		if strings.Contains(w, "yolo allow github on_request runs") {
			t.Fatalf("a named request was reported with the broad allow:\n%s", w)
		}
	}
	if !slices.ContainsFunc(named, func(w string) bool { return strings.HasSuffix(w, want) }) {
		t.Fatalf("warnings = %q\nwant one to end with:\n%s", warns, want)
	}
}

// The declaration line is given only when the user config declares no repository for the name:
// writing `repos infra <identity>` over a different identity would retarget the name, which the
// report must not do. A different identity is named instead.
func TestANamedSkipDeclaresTheRepositoryOnlyWhenTheUserDeclaresNone(t *testing.T) {
	cases := []struct {
		name, user string
		wantDecl   bool
		wantNote   string
	}{
		{"user declares nothing for the name", "", true, ""},
		{"user declares the same identity", "[repos.infra]\nrepo = \"github.com/acme/infra\"\n", false, ""},
		{"user declares another identity", "[repos.infra]\nrepo = \"github.com/other/infra\"\n", false,
			"repos.infra is your config's name for github.com/other/infra"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ws := skippedBrokerFixture(t, c.user+"\n[allow.github]\nalways = [\"read\"]\n", namedSkipRequests)
			merged, err := LoadConfig(ws, true, discard)
			if err != nil {
				t.Fatal(err)
			}
			_, warns := ValidateConfig(merged, ws, nil)
			var block string
			for _, w := range warns {
				if strings.Contains(w, "repos.infra.github") {
					block = w
				}
			}
			if got := strings.Contains(block, "yolo allow repos infra github.com/acme/infra"); got != c.wantDecl {
				t.Fatalf("declaration line present = %v, want %v:\n%s", got, c.wantDecl, block)
			}
			if c.wantNote != "" && !strings.Contains(block, c.wantNote) {
				t.Fatalf("the report lacks %q:\n%s", c.wantNote, block)
			}
		})
	}
}

// A named request written in a file outside the workspace is refused by the gate's read, with its
// key: the same WW-D17 rule `brokered` and `requests` follow, so the outside-writer walk must reach
// `repos` too.
func TestAReadWorkspaceForGateRefusesANamedRequestFromOutsideTheWorkspace(t *testing.T) {
	ws := skippedBrokerFixture(t, "[allow.github]\nalways = [\"read\"]\n", "")
	outside := filepath.Join(filepath.Dir(ws), "outside.toml")
	if err := os.WriteFile(outside, []byte(namedSkipRequests), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(ws, paths.WorkspaceConfigTOMLName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, paths.WorkspaceConfigTOMLName)); err != nil {
		t.Fatal(err)
	}
	_, err := ReadWorkspaceForGate(ws)
	if err == nil || !strings.Contains(err.Error(), "config.repos") || !strings.Contains(err.Error(), "outside this workspace") {
		t.Fatalf("a named request from outside the workspace was not refused: %v", err)
	}
}

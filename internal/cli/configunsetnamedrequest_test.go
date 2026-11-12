package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const namedRequestUnsetProject = "[repos.infra]\nrepo = \"github.com/acme/infra\"\n\n" +
	"[repos.infra.requests.github]\ncreate_pr = true\nruns = [\"deploy.yml\", \"ci.yml\"]\n"

// `yolo config unset repos.<name>.requests.<source>.<capability>` removes a request a named
// repository holds: a capability's whole line, or one item of its list. A dry run and a line
// already gone write nothing.
func TestPublicConfigUnsetRemovesANamedRepositoryRequest(t *testing.T) {
	fx := newNamedVerbFixture(t, namedRequestUnsetProject, requestReposAllow)
	const key = "repos.infra.requests.github.create_pr"

	rc, out, errs := captureStd(t, func() int { return Main([]string{"yolo", "config", "unset", key, "--dry-run"}) })
	if rc != 0 || !strings.Contains(out, "Dry run; nothing was written.") || !strings.Contains(out, "create_pr") {
		t.Fatalf("dry run rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, fx.project); got != namedRequestUnsetProject {
		t.Fatalf("a dry run wrote the project file:\n%s", got)
	}

	rc, out, errs = captureStd(t, func() int { return Main([]string{"yolo", "config", "unset", key}) })
	assertPublicPostWriteCheck(t, rc, out, errs)
	want := strings.Replace(namedRequestUnsetProject, "create_pr = true\n", "", 1)
	if got := readFile(t, fx.project); got != want {
		t.Fatalf("unset %s left the project file as:\n%s\nwant:\n%s", key, got, want)
	}

	rc, out, errs = captureStd(t, func() int { return Main([]string{"yolo", "config", "unset", key}) })
	if rc != 0 || !strings.Contains(out, "No change; no write.") {
		t.Fatalf("a second unset rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, fx.project); got != want {
		t.Fatalf("a second unset wrote the project file:\n%s", got)
	}

	rc, out, errs = captureStd(t, func() int {
		return Main([]string{"yolo", "config", "unset", "repos.infra.requests.github.runs[0]", "--project"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := readFile(t, fx.project)
	if strings.Contains(got, "deploy.yml") || !strings.Contains(got, `runs = ["ci.yml"]`) ||
		!strings.Contains(got, "[repos.infra]\nrepo = \"github.com/acme/infra\"\n") {
		t.Fatalf("unset runs[0] did not remove deploy.yml alone:\n%s", got)
	}
}

// A list capability goes whole when the key names it, and the repository's other request stays.
func TestPublicConfigUnsetRemovesANamedRepositoryRequestList(t *testing.T) {
	fx := newNamedVerbFixture(t, namedRequestUnsetProject, requestReposAllow)
	userBefore, fragmentBefore := readFile(t, fx.userConfig), readFile(t, fx.fragment)

	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "config", "unset", "repos.infra.requests.github.runs"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	want := strings.Replace(namedRequestUnsetProject, "runs = [\"deploy.yml\", \"ci.yml\"]\n", "", 1)
	if got := readFile(t, fx.project); got != want {
		t.Fatalf("unset runs left the project file as:\n%s\nwant:\n%s", got, want)
	}
	if readFile(t, fx.userConfig) != userBefore || readFile(t, fx.fragment) != fragmentBefore {
		t.Fatal("a project unset wrote the user config")
	}
}

// --local removes the line the workspace's local file holds, and leaves the project file alone.
func TestPublicConfigUnsetRemovesANamedRepositoryRequestFromTheLocalFile(t *testing.T) {
	fx := newNamedVerbFixture(t, namedRequestUnsetProject, requestReposAllow)
	local := filepath.Join(fx.workspace, paths.WorkspaceLocalConfigTOMLName)
	const localText = "[repos.infra.requests.github]\ncreate_issue = true\nruns = [\"local.yml\"]\n"
	if err := os.WriteFile(local, []byte(localText), 0o600); err != nil {
		t.Fatal(err)
	}

	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "config", "unset", "repos.infra.requests.github.runs", "--local"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	if got := readFile(t, local); got != "[repos.infra.requests.github]\ncreate_issue = true\n" {
		t.Fatalf("unset --local left the local file as:\n%s", got)
	}
	if got := readFile(t, fx.project); got != namedRequestUnsetProject {
		t.Fatalf("unset --local wrote the project file:\n%s", got)
	}
}

// Every unset a named repository cannot take is refused with its next step, and writes nothing.
func TestPublicConfigUnsetOnANamedRepositoryRefusesWithoutWriting(t *testing.T) {
	fx := newNamedVerbFixture(t, namedRequestUnsetProject, requestReposAllow)
	userBefore, fragmentBefore := readFile(t, fx.userConfig), readFile(t, fx.fragment)

	for _, tc := range []struct {
		argv []string
		want []string
	}{
		// The repo line is the identity the retained requests are written for.
		{[]string{"yolo", "config", "unset", "repos.infra.repo"}, []string{
			"yolo config unset repos.infra.requests.github.create_pr",
			"yolo config unset repos.infra.requests.github.runs",
		}},
		{[]string{"yolo", "config", "unset", "repos.infra.requests.github.runz"}, []string{"runz", "create_pr", "runs"}},
		{[]string{"yolo", "config", "unset", "repos.infra.requests.nope.runs"}, []string{"nope", "github"}},
		{[]string{"yolo", "config", "unset", "repos.infra.requests.github.read"}, []string{"yolo allow github read"}},
		{[]string{"yolo", "config", "unset", "repos.ghost.requests.github.create_pr"}, []string{"[repos.ghost]", "infra"}},
		{[]string{"yolo", "config", "unset", `repos."in fra".requests.github.create_pr`}, []string{"is not a name"}},
		{[]string{"yolo", "config", "unset", "repos.infra.requests.github.runs[7]"}, []string{"holds 2 items"}},
		{[]string{"yolo", "config", "unset", "repos.infra.requests.github.create_pr[0]"}, []string{"is not a list"}},
		{[]string{"yolo", "config", "unset", "repos.infra.requests.github"}, nil},
		{[]string{"yolo", "config", "unset", "repos.infra.requests.github.create_pr", "--user"}, []string{"workspace-only"}},
		{[]string{"yolo", "config", "set", "repos.infra.requests.github.create_pr", "false"}, nil},
		{[]string{"yolo", "config", "add", "repos.infra.requests.github.runs", "nightly.yml"}, nil},
	} {
		rc, out, errs := captureStd(t, func() int { return Main(tc.argv) })
		if rc == 0 {
			t.Fatalf("%v accepted; stdout=%q stderr=%q", tc.argv, out, errs)
		}
		for _, want := range tc.want {
			if !strings.Contains(errs, want) {
				t.Fatalf("%v refusal lacks %q: %q", tc.argv, want, errs)
			}
		}
		if got := readFile(t, fx.project); got != namedRequestUnsetProject {
			t.Fatalf("%v was refused and still wrote the project file:\n%s", tc.argv, got)
		}
		if readFile(t, fx.userConfig) != userBefore || readFile(t, fx.fragment) != fragmentBefore {
			t.Fatalf("%v was refused and still wrote the user config", tc.argv)
		}
	}
}

// The schema types nothing under a named repository's broker table, so the generic set and add
// have no value type there: the request verb stays the one author of those lines.
func TestTheSchemaTypesNothingUnderANamedRepositoryBroker(t *testing.T) {
	sawAny := false
	for _, rule := range config.ReadableSchemaKeys() {
		parts := strings.Split(rule.Path, ".")
		if len(parts) < 4 || parts[0] != "repos" || parts[2] != "requests" {
			continue
		}
		if len(parts) > 4 {
			t.Fatalf("the schema types %s; a named repository's capabilities are typed by the broker's declaration", rule.Path)
		}
		if rule.Path == "repos.*.requests.*" {
			sawAny = rule.Type == config.SchemaTypeAny && rule.Scope == config.SchemaScopeWorkspaceOnly
		}
	}
	if !sawAny {
		t.Fatal("repos.*.requests.* is not the workspace-only any row the named unset reads")
	}
}

// With no repo line a name is the clone's remote of that name, so the repo line goes only when
// that remote is there to name the repository of the requests that stay.
func TestPublicConfigUnsetOfARepoLineNeedsARemoteForItsRequests(t *testing.T) {
	fx := newNamedVerbFixture(t, namedRequestUnsetProject, requestReposAllow)
	gitConfig := filepath.Join(fx.workspace, ".git", "config")
	if err := os.MkdirAll(filepath.Dir(gitConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	writeRemote := func(name string) {
		t.Helper()
		text := "[remote \"" + name + "\"]\n\turl = https://github.com/acme/infra.git\n"
		if err := os.WriteFile(gitConfig, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	argv := []string{"yolo", "config", "unset", "repos.infra.repo"}

	writeRemote("origin")
	rc, out, errs := captureStd(t, func() int { return Main(argv) })
	if rc == 0 || !strings.Contains(errs, "no remote named infra") ||
		!strings.Contains(errs, "yolo config unset repos.infra.requests.github.runs") {
		t.Fatalf("unset of the repo line with no remote named infra: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, fx.project); got != namedRequestUnsetProject {
		t.Fatalf("a refused unset wrote the project file:\n%s", got)
	}

	writeRemote("infra")
	rc, out, errs = captureStd(t, func() int { return Main(argv) })
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := readFile(t, fx.project)
	if strings.Contains(got, "repo =") || !strings.Contains(got, "create_pr = true") || !strings.Contains(got, "deploy.yml") {
		t.Fatalf("unset of the repo line with a remote named infra left:\n%s", got)
	}
}

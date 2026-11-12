package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

const requestReposAllow = "[allow.git]\nalways = [\"fetch\"]\n\n[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"create_pr\"]\n"

func TestRequestReposDeclaresANamedRepository(t *testing.T) {
	fx := newNamedVerbFixture(t, "", requestReposAllow)
	userBefore, fragmentBefore := readFile(t, fx.userConfig), readFile(t, fx.fragment)

	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos", "infra", "github.com/acme/infra"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := readFile(t, fx.project)
	if !strings.Contains(got, "[repos.infra]") || !strings.Contains(got, `repo = "github.com/acme/infra"`) {
		t.Fatalf("project file was not written:\n%s\nstdout=%q stderr=%q", got, out, errs)
	}
	if strings.Contains(got, "[requests") {
		t.Fatalf("declaring a repository wrote a request:\n%s", got)
	}
	if !strings.Contains(out, "yolo config apply") {
		t.Fatalf("the verb did not name the host step: %q", out)
	}

	local := filepath.Join(fx.workspace, config.WorkspaceLocalTOMLConfigName)
	rc, out, errs = captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos", "notes", "github.com/acme/notes", "--local"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	if l := readFile(t, local); !strings.Contains(l, "[repos.notes]") || !strings.Contains(l, `repo = "github.com/acme/notes"`) {
		t.Fatalf("--local did not write the local file:\n%s", l)
	}
	if strings.Contains(readFile(t, fx.project), "notes") {
		t.Fatal("--local wrote the project file")
	}
	if readFile(t, fx.userConfig) != userBefore || readFile(t, fx.fragment) != fragmentBefore {
		t.Fatal("yolo request edited the user config")
	}

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"yolo", "request", "repos", "infra2", "acme/infra"}, []string{"github.com/acme/infra", "yolo request repos infra2"}},
		{[]string{"yolo", "request", "repos", "bad", "*/acme/infra"}, []string{"host", "yolo request repos bad"}},
		{[]string{"yolo", "request", "repos", "bad", "github.com/ac*/infra"}, []string{"whole", "yolo request repos bad"}},
		{[]string{"yolo", "request", "repos", "infra"}, []string{"yolo request repos <alias> <host>/<path>"}},
	} {
		before := readFile(t, fx.project)
		rc, out, errs := captureStd(t, func() int { return Main(tc.args) })
		if rc == 0 {
			t.Fatalf("%v rc=0; stdout=%q stderr=%q", tc.args, out, errs)
		}
		for _, want := range tc.want {
			if !strings.Contains(errs, want) {
				t.Fatalf("%v refusal lacks %q: %q", tc.args, want, errs)
			}
		}
		if readFile(t, fx.project) != before {
			t.Fatalf("%v was refused and still wrote the project file", tc.args)
		}
	}
}

func TestRequestReposAliasWritesTheQualifiedLine(t *testing.T) {
	fx := newNamedVerbFixture(t, "[repos.infra]\nrepo = \"github.com/acme/infra\"\n", requestReposAllow)

	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos.infra.github", "runs", "deploy.yml"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	rc, out, errs = captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos.infra.github", "create_pr"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := readFile(t, fx.project)
	for _, want := range []string{"[repos.infra.requests.github]", `runs = ["deploy.yml"]`, "create_pr = true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("project file lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[requests.github]") {
		t.Fatalf("a qualified request wrote the unqualified table:\n%s", got)
	}
	if strings.Contains(out, "runs without it") {
		t.Fatalf("an allowed request was reported as skipped: %q", out)
	}

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"yolo", "request", "repos.nope.github", "runs", "deploy.yml"}, []string{"yolo request repos nope <host>/<path>"}},
		{[]string{"yolo", "request", "repos.origin.github", "create_pr"}, []string{"yolo request github create_pr"}},
		{[]string{"yolo", "request", "repos.infra.github", "runs"}, []string{"workflow file name", "yolo request repos.infra.github runs nightly.yml"}},
		{[]string{"yolo", "request", "repos.infra.github", "pushh"}, []string{"create_pr", "runs", "yolo request repos.infra.github"}},
		{[]string{"yolo", "request", "repos.infra.github", "read"}, []string{"yolo allow github read"}},
		{[]string{"yolo", "request", "repos.infra.github", "repos", "acme/x"}, []string{"yolo request repos <alias> <host>/<path>"}},
		{[]string{"yolo", "request", "repos.infra", "push", "main"}, []string{"yolo request git push main"}},
	} {
		before := readFile(t, fx.project)
		rc, out, errs := captureStd(t, func() int { return Main(tc.args) })
		if rc != 1 {
			t.Fatalf("%v rc=%d, want 1; stdout=%q stderr=%q", tc.args, rc, out, errs)
		}
		for _, want := range tc.want {
			if !strings.Contains(errs, want) {
				t.Fatalf("%v refusal lacks %q: %q", tc.args, want, errs)
			}
		}
		if readFile(t, fx.project) != before {
			t.Fatalf("%v was refused and still wrote the project file", tc.args)
		}
	}
}

// A qualified request above the ceiling is written and reported skipped, naming the scoped allow
// first: the broad one would allow it on every repository.
func TestRequestReposAliasOutsideTheCeilingNamesTheScopedAllow(t *testing.T) {
	fx := newNamedVerbFixture(t, "[repos.infra]\nrepo = \"github.com/acme/infra\"\n", "[allow.github]\nalways = [\"read\"]\n")
	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos.infra.github", "runs", "deploy.yml"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	if got := readFile(t, fx.project); !strings.Contains(got, `runs = ["deploy.yml"]`) {
		t.Fatalf("a request above the ceiling was not written:\n%s", got)
	}
	for _, want := range []string{"runs without it", "yolo allow repos <alias> github.com/acme/infra", "yolo allow repos.<alias>.github on_request runs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the skip report lacks %q: %q", want, out)
		}
	}
}

func TestConfigWhyNamesTheReposVerbs(t *testing.T) {
	newNamedVerbFixture(t, "", "")
	for key, want := range map[string]string{
		"repos.infra.repo":                         "yolo request repos infra <host>/<path>",
		"repos.infra.requests.github.runs":         "set it with `yolo request repos.infra.github runs <workflow file>`",
		"repos.infra.requests.github.create_pr":    "set it with `yolo request repos.infra.github create_pr`",
		"repos.infra.requests.github.approve_runs": "set it with `yolo request repos.infra.github approve_runs <actor login>`",
		"repos.mine.allow.github.on_request":       "set it with `yolo allow repos.mine.github on_request <capability>`",
	} {
		rc, out, errs := captureStd(t, func() int { return Main([]string{"yolo", "config", "why", key}) })
		if rc != 0 || !strings.Contains(out, want) {
			t.Fatalf("config why %s rc=%d stdout=%q stderr=%q; want %q", key, rc, out, errs, want)
		}
		if strings.Contains(out, "yolo config set") {
			t.Fatalf("config why %s offers the generic verb beside the named one: %q", key, out)
		}
	}
}

package cli

import (
	"strings"
	"testing"
)

// The generic config verbs write a named repository's line, and refuse every key a named repository
// cannot take, without writing the file (GW-D76). The requests under a name are written by
// `yolo request repos.<alias>.<source>`, not by the generic set; the generic unset removes one
// (configunsetnamedrequest_test.go), and refuses a capability the broker does not declare.
func TestPublicConfigOnANamedRepositoryWritesItsLineAndRefusesBadKeys(t *testing.T) {
	fx := newNamedVerbFixture(t, "[repos.infra]\nrepo = \"github.com/acme/infra\"\n", requestReposAllow)

	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "config", "set", "repos.infra.repo", "github.com/acme/other"})
	})
	if rc != 0 {
		t.Fatalf("config set repos.infra.repo rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, fx.project); !strings.Contains(got, `repo = "github.com/acme/other"`) {
		t.Fatalf("config set did not write the repository line:\n%s", got)
	}

	for _, argv := range [][]string{
		{"yolo", "config", "set", "repos.infra.repo", "acme/other"},
		{"yolo", "config", "set", "repos.infra.repo", "github.com/ac*/other"},
		{"yolo", "config", "set", "repos.infra.requests.github.create_pr", "true"},
		{"yolo", "config", "set", "repos.infra.requests.nope.runs", "deploy.yml"},
		{"yolo", "config", "set", "repos.infra.requests.github.runz", "x"},
		{"yolo", "config", "add", "repos.infra.requests.github.runs", "deploy.yml"},
		{"yolo", "config", "unset", "repos.infra.requests.github.runz"},
	} {
		before := readFile(t, fx.project)
		rc, out, errs := captureStd(t, func() int { return Main(argv) })
		if rc == 0 {
			t.Fatalf("%v accepted; stdout=%q stderr=%q", argv, out, errs)
		}
		if readFile(t, fx.project) != before {
			t.Fatalf("%v was refused and still wrote the project file", argv)
		}
	}
}

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// TestAReleaseBuildPrintsItsVersionAndCommit pins the owner's 2026-10-16 ruling
// (docs/reference/self-update.md OQ-DA2): "yes, releases name the hash too. hashes are
// _actually_ immutable." It renders .goreleaser.yaml's real ldflags for tag v0.14.0 at
// testSHA through text/template (GoReleaser's template engine, whose builtins include
// `slice`), and outside -short builds cmd/yolo with them and runs it: the release binary
// must print `yolo-jail 0.14.0+g<hash12>` and name the commit in `--version --full`.
func TestAReleaseBuildPrintsItsVersionAndCommit(t *testing.T) {
	var config struct {
		Builds []struct {
			ID      string   `yaml:"id"`
			Ldflags []string `yaml:"ldflags"`
		} `yaml:"builds"`
	}
	if err := yaml.Unmarshal(mustRead(t, ".goreleaser.yaml"), &config); err != nil {
		t.Fatal(err)
	}
	var yolo []string
	for _, b := range config.Builds {
		if b.ID == "yolo" {
			yolo = b.Ldflags
		}
	}
	if len(yolo) == 0 {
		t.Fatal(".goreleaser.yaml has no `yolo` build with ldflags")
	}
	data := map[string]string{"Version": "0.14.0", "FullCommit": testSHA, "ShortCommit": testSHA[:7]}
	var rendered []string
	for _, flag := range yolo {
		tmpl, err := template.New("ldflag").Option("missingkey=error").Parse(flag)
		if err != nil {
			t.Fatalf("ldflag %q does not parse as a template: %v", flag, err)
		}
		var b strings.Builder
		if err := tmpl.Execute(&b, data); err != nil {
			t.Fatalf("ldflag %q does not render: %v", flag, err)
		}
		rendered = append(rendered, b.String())
	}
	ldflags := strings.Join(rendered, " ")
	hash := testSHA[:12]
	for _, want := range []string{
		"internal/version.buildVersion=0.14.0+g" + hash,
		"internal/version.GitCommit=" + hash,
	} {
		if !strings.Contains(ldflags, want) {
			t.Errorf("the release ldflags %q do not stamp %q", ldflags, want)
		}
	}
	// The release gates take the TAG, which is unchanged: v0.14.0 is still accepted as
	// version 0.14.0, and a stamped binary string is not a release version.
	if v, err := normalizeVersion("v0.14.0"); err != nil || v != "0.14.0" {
		t.Errorf("normalizeVersion(v0.14.0) = %q, %v; the tag gate must still accept it", v, err)
	}
	if _, err := normalizeVersion("0.14.0+g" + hash); err == nil {
		t.Error("normalizeVersion accepted a binary's stamped string as a release version")
	}
	if testing.Short() {
		return
	}

	bin := filepath.Join(t.TempDir(), "yolo")
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "./cmd/yolo")
	build.Dir = repositoryRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build with the release ldflags: %v\n%s", err, out)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "YOLO_VERSION=") { // a jail's launcher version would win
			env = append(env, kv)
		}
	}
	run := exec.Command(bin, "--version", "--full")
	run.Env = env
	out, err := run.Output()
	if err != nil {
		t.Fatalf("yolo --version --full: %v", err)
	}
	lines := strings.Split(string(out), "\n")
	if lines[0] != "yolo-jail 0.14.0+g"+hash {
		t.Errorf("a release binary prints %q, want %q", lines[0], "yolo-jail 0.14.0+g"+hash)
	}
	if !strings.Contains(string(out), "\ncommit: "+hash+"\n") {
		t.Errorf("a release binary's --full does not name its commit:\n%s", out)
	}
}

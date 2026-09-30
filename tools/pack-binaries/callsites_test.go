package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The pin tool's CALL SITES are the release's three gates and the recipe that pins
// (docs/design/broker-as-a-pack.md §14.4, steps 4 to 6). Each runs only when a release is cut,
// so nothing but the next release would notice one being deleted; these tests read the files
// and fail instead.

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// justRecipe returns the body of the Justfile recipe name: the indented lines under its header.
func justRecipe(t *testing.T, name string) []string {
	t.Helper()
	var body []string
	in := false
	for _, line := range strings.Split(repoFile(t, "Justfile"), "\n") {
		switch {
		case !in && (strings.HasPrefix(line, name+":") || strings.HasPrefix(line, name+" ")):
			in = true
		case in && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			return body
		case in:
			body = append(body, strings.TrimSpace(line))
		}
	}
	if !in {
		t.Fatalf("the Justfile has no %s recipe", name)
	}
	return body
}

func indexOf(lines []string, sub string) int {
	for i, l := range lines {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

func TestJustPinPackBinariesRunsThePinVerb(t *testing.T) {
	body := justRecipe(t, "pin-pack-binaries")
	if indexOf(body, `go run ./tools/pack-binaries pin "{{version}}"`) < 0 {
		t.Errorf("`just pin-pack-binaries` no longer runs the pin tool's pin verb:\n%s", strings.Join(body, "\n"))
	}
}

// `just release` refuses before the tag, after the changelog gate, and says nothing was tagged.
func TestJustReleaseChecksThePinsBeforeItTags(t *testing.T) {
	body := justRecipe(t, "release")
	check := indexOf(body, `go run ./tools/pack-binaries check "$v"`)
	changelog := indexOf(body, `scripts/changelog-section.sh "{{version}}"`)
	tag := indexOf(body, "git tag -a")
	switch {
	case check < 0:
		t.Fatalf("`just release` does not run the pin tool's check:\n%s", strings.Join(body, "\n"))
	case changelog < 0 || tag < 0:
		t.Fatalf("`just release` lost its changelog gate or its tag; this test's landmarks are gone")
	case !(changelog < check && check < tag):
		t.Errorf("the pin check is at line %d of `just release`, and must sit after the changelog "+
			"gate (%d) and before the tag (%d)", check, changelog, tag)
	}
	if !strings.HasPrefix(body[check], "if ! ") {
		t.Fatalf("the check's failure no longer stops the recipe: %q", body[check])
	}
	end := check
	for end < len(body) && body[end] != "fi" {
		end++
	}
	refusal := strings.Join(body[check:end], "\n")
	for _, want := range []string{"Nothing has been tagged.", "just pin-pack-binaries", "exit 1"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the pin check's refusal does not say %q:\n%s", want, refusal)
		}
	}
	if indexOf(body, "pack-binaries pin") >= 0 {
		t.Error("`just release` runs pin, which writes the manifests after the tree was checked clean")
	}
}

// goreleaser stages the verified builds before it builds anything, outside dist/, and uploads
// and checksums exactly what it staged.
func TestGoreleaserStagesThePackBinariesAndPublishesThem(t *testing.T) {
	var cfg struct {
		Before struct {
			Hooks []string `yaml:"hooks"`
		} `yaml:"before"`
		Checksum struct {
			ExtraFiles []struct {
				Glob string `yaml:"glob"`
			} `yaml:"extra_files"`
		} `yaml:"checksum"`
		Release struct {
			ExtraFiles []struct {
				Glob string `yaml:"glob"`
			} `yaml:"extra_files"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal([]byte(repoFile(t, ".goreleaser.yaml")), &cfg); err != nil {
		t.Fatal(err)
	}
	const prefix = "go run ./tools/pack-binaries stage {{ .Version }} "
	dir := ""
	for _, h := range cfg.Before.Hooks {
		if rest, ok := strings.CutPrefix(h, prefix); ok {
			dir = strings.TrimSpace(rest)
		}
	}
	if dir == "" {
		t.Fatalf("no goreleaser before-hook runs %q<dir>: %q", prefix, cfg.Before.Hooks)
	}
	if dir == "dist" || strings.HasPrefix(dir, "dist/") || strings.HasPrefix(dir, "./dist") {
		t.Errorf("the pack binaries stage into %s, which goreleaser empties after the hooks", dir)
	}
	glob := "./" + strings.TrimPrefix(dir, "./") + "/*"
	for name, files := range map[string][]struct {
		Glob string `yaml:"glob"`
	}{"release": cfg.Release.ExtraFiles, "checksum": cfg.Checksum.ExtraFiles} {
		found := false
		for _, f := range files {
			found = found || f.Glob == glob
		}
		if !found {
			t.Errorf("%s.extra_files does not name %s, so the staged builds are not %s", name, glob,
				map[string]string{"release": "uploaded", "checksum": "in checksums.txt"}[name])
		}
	}
	top, _, _ := strings.Cut(strings.TrimPrefix(dir, "./"), "/")
	ignored := false
	for _, line := range strings.Split(repoFile(t, ".gitignore"), "\n") {
		line = strings.TrimSpace(line)
		ignored = ignored || line == top+"/" || line == "/"+top+"/" || line == "/"+top
	}
	if !ignored {
		t.Errorf("%s is not git-ignored, and goreleaser refuses to release from a dirty tree", top)
	}
}

type workflow struct {
	Jobs map[string]struct {
		Needs yaml.Node `yaml:"needs"`
		If    string    `yaml:"if"`
		Steps []struct {
			Uses string `yaml:"uses"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func parseWorkflow(t *testing.T, rel string) workflow {
	t.Helper()
	var wf workflow
	if err := yaml.Unmarshal([]byte(repoFile(t, rel)), &wf); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return wf
}

func needs(n yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
		return out
	}
	return nil
}

// PyPI runs beside the release, not after it, so it checks the pins itself, in the gate every
// publishing job waits on.
func TestPublishChecksThePinsBeforeAnythingIsPublished(t *testing.T) {
	const rel = ".github/workflows/publish.yml"
	wf := parseWorkflow(t, rel)
	gate, ok := wf.Jobs["release-notes"]
	if !ok {
		t.Fatalf("%s has no release-notes job", rel)
	}
	setup, check := -1, -1
	for i, s := range gate.Steps {
		if strings.HasPrefix(s.Uses, "actions/setup-go@") && setup < 0 {
			setup = i
		}
		if strings.Contains(s.Run, `go run ./tools/pack-binaries check "$version"`) {
			check = i
		}
	}
	if check < 0 {
		t.Fatalf("%s's release-notes gate does not run the pin tool's check", rel)
	}
	if setup < 0 || setup > check {
		t.Errorf("%s runs the pin check before it sets up Go", rel)
	}
	// Every job reaches the gate through `needs`, so none publishes past a refusal.
	var reaches func(job string, seen map[string]bool) bool
	reaches = func(job string, seen map[string]bool) bool {
		if job == "release-notes" {
			return true
		}
		if seen[job] {
			return false
		}
		seen[job] = true
		for _, n := range needs(wf.Jobs[job].Needs) {
			if reaches(n, seen) {
				return true
			}
		}
		return false
	}
	for job := range wf.Jobs {
		if !reaches(job, map[string]bool{}) {
			t.Errorf("%s's %s job does not wait on release-notes, so it can publish a version "+
				"whose pins the release refused", rel, job)
		}
	}
}

// The release's goreleaser job has Go before the hook needs it, and the Homebrew formula is
// pushed only after goreleaser — and so the stage hook — succeeded.
func TestReleaseRunsTheStageHookBeforeHomebrew(t *testing.T) {
	const rel = ".github/workflows/release.yml"
	wf := parseWorkflow(t, rel)
	g := wf.Jobs["goreleaser"]
	setup, run := -1, -1
	for i, s := range g.Steps {
		if strings.HasPrefix(s.Uses, "actions/setup-go@") && setup < 0 {
			setup = i
		}
		if strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action@") {
			run = i
		}
	}
	if run < 0 || setup < 0 || setup > run {
		t.Errorf("%s's goreleaser job must set up Go before goreleaser runs its hooks (setup %d, goreleaser %d)",
			rel, setup, run)
	}
	brew := wf.Jobs["update-homebrew"]
	if n := needs(brew.Needs); len(n) != 1 || n[0] != "goreleaser" ||
		!strings.Contains(brew.If, "needs.goreleaser.result == 'success'") {
		t.Errorf("%s's update-homebrew job no longer waits on goreleaser's success (needs %v, if %q)",
			rel, n, brew.If)
	}
}

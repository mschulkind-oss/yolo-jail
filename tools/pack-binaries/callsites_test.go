package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The pin tool's CALL SITES are the release's three gates and the recipe that pins
// (docs/design/broker-as-a-pack.md §14.4, steps 4 to 6), and what main pins between releases
// (step 7, BP-D15): the check `just check-ci` runs, the seed `just install` runs, and the seed
// the integration harness runs. Each runs only when a release is cut, a host installs, or a full
// container run starts — none of them in the short suite — so nothing would notice one being
// deleted; these tests read the files and fail instead.

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

// justDeps returns the dependencies named on the Justfile recipe name's header line.
func justDeps(t *testing.T, name string) []string {
	t.Helper()
	for _, line := range strings.Split(repoFile(t, "Justfile"), "\n") {
		if rest, ok := strings.CutPrefix(line, name+":"); ok {
			return strings.Fields(rest)
		}
	}
	t.Fatalf("the Justfile has no %s recipe", name)
	return nil
}

// justParams returns the parameters on the Justfile recipe name's header line.
func justParams(t *testing.T, name string) []string {
	t.Helper()
	for _, line := range strings.Split(repoFile(t, "Justfile"), "\n") {
		if rest, ok := strings.CutPrefix(line, name+" "); ok {
			params, _, _ := strings.Cut(rest, ":")
			return strings.Fields(params)
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

// `just pin-pack-binaries` takes its version as optional: with one it is the release's pin, and
// with none the digest-only pin the check-ci refusal names (BP-D15).
func TestJustPinPackBinariesRunsThePinVerb(t *testing.T) {
	body := justRecipe(t, "pin-pack-binaries")
	if indexOf(body, `go run ./tools/pack-binaries pin {{version}}`) < 0 {
		t.Errorf("`just pin-pack-binaries` no longer runs the pin tool's pin verb:\n%s", strings.Join(body, "\n"))
	}
	if p := justParams(t, "pin-pack-binaries"); len(p) != 1 || p[0] != "*version" {
		t.Errorf("`just pin-pack-binaries` takes %v, want an optional version (*version), so the "+
			"bare command check-ci names runs", p)
	}
}

// THE LANDING GATE REFUSES A PIN THE TREE NO LONGER REPRODUCES (BP-D15): `just check-ci` — and
// `just check`, which `just done` runs — depend on a recipe running the digest-only check, with
// no version, so a url naming an earlier release passes.
func TestJustCheckCIChecksThePinsAgainstTheTree(t *testing.T) {
	const recipe = "check-pack-binaries"
	for _, gate := range []string{"check-ci", "check"} {
		if !contains(justDeps(t, gate), recipe) {
			t.Errorf("`just %s` no longer depends on `just %s` (%v), so a pin the tree no longer "+
				"reproduces lands", gate, recipe, justDeps(t, gate))
		}
	}
	body := justRecipe(t, recipe)
	i := indexOf(body, "go run ./tools/pack-binaries check")
	if i < 0 {
		t.Fatalf("`just %s` does not run the pin tool's check:\n%s", recipe, strings.Join(body, "\n"))
	}
	if body[i] != "go run ./tools/pack-binaries check" {
		t.Errorf("`just %s` runs %q: between releases the check takes no version, or every url "+
			"naming the last release fails it", recipe, body[i])
	}
}

// `just install` BUILDS THE TREE'S OFFICIAL PROGRAMS INTO THE CACHE (BP-D15), re-pinning a moved
// one: after its in-jail refusal, before the version stamp (so a re-pin reads as -dirty) and
// before `go install` embeds the manifests — and a failure installs nothing.
func TestJustInstallSeedsTheTreesProgramsBeforeItInstalls(t *testing.T) {
	body := justRecipe(t, "install")
	seed := indexOf(body, "go run ./tools/pack-binaries seed --repin")
	jail := indexOf(body, `if [ -n "${YOLO_VERSION:-}" ]; then`)
	stamp := indexOf(body, `VERSION="$(git describe`)
	install := indexOf(body, "go install ")
	bundle := indexOf(body, "stage-source-bundle.sh")
	switch {
	case seed < 0:
		t.Fatalf("`just install` does not seed the pack-binary cache:\n%s", strings.Join(body, "\n"))
	case jail < 0 || stamp < 0 || install < 0 || bundle < 0:
		t.Fatalf("`just install` lost a landmark this test orders the seed by (jail %d, stamp %d, "+
			"go install %d, bundle %d)", jail, stamp, install, bundle)
	case !(jail < seed && seed < stamp && seed < install && seed < bundle):
		t.Errorf("the seed is at line %d of `just install`; it must follow the in-jail refusal "+
			"(%d) and precede the version stamp (%d), `go install` (%d) and the bundle (%d)",
			seed, jail, stamp, install, bundle)
	}
	if !strings.HasPrefix(body[seed], "if ! ") {
		t.Fatalf("the seed's failure no longer stops the install: %q", body[seed])
	}
	end := seed
	for end < len(body) && body[end] != "fi" {
		end++
	}
	refusal := strings.Join(body[seed:end], "\n")
	for _, want := range []string{"Nothing has been installed", "just install", "exit 1"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the seed's refusal does not say %q:\n%s", want, refusal)
		}
	}
}

// THE INTEGRATION HARNESS SEEDS ITS RUN'S CACHE (§14.4 step 7's B): runSuite calls
// seedPackBinaries once the run store exists and before the first launch, and that runs the
// tool's seed verb, without --repin. Read from the source, since the call runs only in a full
// container run.
func TestTheIntegrationHarnessSeedsItsRunsCache(t *testing.T) {
	calls := funcCalls(t, "integration/harness_test.go", "runSuite")
	// The LAST of each: the short path returns through an m.Run of its own before any setup.
	at := func(name string) int {
		for i := len(calls) - 1; i >= 0; i-- {
			if calls[i] == name {
				return i
			}
		}
		return -1
	}
	seed, store, image, warm, mrun := at("seedPackBinaries"), at("setUpRunStore"),
		at("ensureJailImage"), at("warmJail"), at("m.Run")
	switch {
	case seed < 0:
		t.Fatalf("runSuite no longer calls seedPackBinaries: %v", calls)
	case store < 0 || image < 0 || warm < 0 || mrun < 0:
		t.Fatalf("runSuite lost a landmark this test orders the seed by: %v", calls)
	case !(store < seed && seed < image && seed < warm && seed < mrun):
		t.Errorf("runSuite seeds at call %d; it must follow setUpRunStore (%d) and precede "+
			"ensureJailImage (%d), warmJail (%d) and m.Run (%d): %v", seed, store, image, warm, mrun, calls)
	}

	args := stringArgsOf(t, "integration/packbinaryseed_test.go", "packBinarySeedCmd", "exec.Command")
	if strings.Join(args, " ") != "go run ./tools/pack-binaries seed" {
		t.Errorf("the harness's seed runs %q, want the tool's seed verb with no --repin", args)
	}
}

// funcCalls returns, in source order, every call made in the body of the function fn in the Go
// file rel, as "name" or "recv.name".
func funcCalls(t *testing.T, rel, fn string) []string {
	t.Helper()
	var out []string
	ast.Inspect(funcBody(t, rel, fn), func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			switch f := c.Fun.(type) {
			case *ast.Ident:
				out = append(out, f.Name)
			case *ast.SelectorExpr:
				if x, ok := f.X.(*ast.Ident); ok {
					out = append(out, x.Name+"."+f.Sel.Name)
				}
			}
		}
		return true
	})
	return out
}

// stringArgsOf returns the string-literal arguments of the first call to callee inside fn.
func stringArgsOf(t *testing.T, rel, fn, callee string) []string {
	t.Helper()
	var out []string
	found := false
	ast.Inspect(funcBody(t, rel, fn), func(n ast.Node) bool {
		if found {
			return false
		}
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		s, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := s.X.(*ast.Ident); !ok || x.Name+"."+s.Sel.Name != callee {
			return true
		}
		found = true
		for _, a := range c.Args {
			if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, v)
			}
		}
		return false
	})
	if !found {
		t.Fatalf("%s's %s makes no %s call", rel, fn, callee)
	}
	return out
}

// funcBody is the body of the top-level function fn in the Go file rel.
func funcBody(t *testing.T, rel, fn string) *ast.BlockStmt {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), rel, repoFile(t, rel), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == fn && fd.Recv == nil && fd.Body != nil {
			return fd.Body
		}
	}
	t.Fatalf("%s has no func %s", rel, fn)
	return nil
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

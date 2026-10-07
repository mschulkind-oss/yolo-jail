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

	"github.com/mschulkind-oss/yolo-jail/internal/selfupdate"
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
	if !strings.HasPrefix(body[seed], "if ! ") && !strings.HasPrefix(body[seed], "elif ! ") {
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

// `yolo update` DEPLOYS UPSTREAM'S TREE AS PULLED (BP-D31): its deploy step sets
// selfupdate.InstallKeepTreeEnv, and under it `just install` seeds without --repin, so nothing
// writes the checkout, and a build it cannot seed is reported rather than failing the deploy —
// which would leave a pulled tree with the old binary. The two spellings of the name are held
// together here.
func TestJustInstallKeepsTheTreeForAnUpdate(t *testing.T) {
	body := justRecipe(t, "install")
	branch := indexOf(body, `if [ -n "${`+selfupdate.InstallKeepTreeEnv+`:-}" ]; then`)
	repin := indexOf(body, "go run ./tools/pack-binaries seed --repin")
	if branch < 0 || repin < 0 || branch > repin {
		t.Fatalf("`just install` has no %s branch ahead of its re-pinning seed (branch %d, "+
			"seed --repin %d):\n%s", selfupdate.InstallKeepTreeEnv, branch, repin, strings.Join(body, "\n"))
	}
	kept := strings.Join(body[branch:repin], "\n")
	if !strings.Contains(kept, "go run ./tools/pack-binaries seed;") {
		t.Errorf("the %s branch does not seed without --repin:\n%s", selfupdate.InstallKeepTreeEnv, kept)
	}
	if strings.Contains(kept, "exit 1") || strings.Contains(kept, "--repin") {
		t.Errorf("the %s branch re-pins, or fails the deploy:\n%s", selfupdate.InstallKeepTreeEnv, kept)
	}
	if !strings.Contains(kept, "just install") {
		t.Errorf("the %s branch's warning names no next step:\n%s", selfupdate.InstallKeepTreeEnv, kept)
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

// `just release` refuses before it submits a request, after the changelog and pin gates.
func TestJustReleaseChecksThePinsBeforeItDispatchesTheFrozenRequest(t *testing.T) {
	body := justRecipe(t, "release")
	check := indexOf(body, `go run ./tools/pack-binaries check "$v"`)
	changelog := indexOf(body, `scripts/changelog-section.sh "$v"`)
	dispatch := indexOf(body, "gh workflow run release-request.yml")
	switch {
	case check < 0:
		t.Fatalf("`just release` does not run the pin tool's check:\n%s", strings.Join(body, "\n"))
	case changelog < 0 || dispatch < 0:
		t.Fatalf("`just release` lost its changelog gate or request dispatch; this test's landmarks are gone")
	case !(changelog < check && check < dispatch):
		t.Errorf("the pin check is at line %d; it must follow the changelog gate (%d) and precede the exact-SHA request dispatch (%d)", check, changelog, dispatch)
	}
	if !strings.HasPrefix(body[check], "if ! ") {
		t.Fatalf("the check's failure no longer stops the recipe: %q", body[check])
	}
	end := check
	for end < len(body) && body[end] != "fi" {
		end++
	}
	refusal := strings.Join(body[check:end], "\n")
	for _, want := range []string{"No release request was sent", "just pin-pack-binaries", "exit 1"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the pin check's refusal does not say %q:\n%s", want, refusal)
		}
	}
	if indexOf(body, "git tag") >= 0 || indexOf(body, "git push") >= 0 {
		t.Error("`just release` writes/pushes a tag locally instead of requesting gated publication")
	}
	if !strings.Contains(body[dispatch], `"sha=$sha"`) || !strings.Contains(body[dispatch], `--ref main`) {
		t.Errorf("request dispatch does not carry the frozen SHA on main: %q", body[dispatch])
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
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		Permissions map[string]string `yaml:"permissions"`
		Needs       yaml.Node         `yaml:"needs"`
		If          string            `yaml:"if"`
		Steps       []struct {
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
			Env  map[string]string `yaml:"env"`
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

// Publication is authorized by trusted main and the successful pin-checked
// original Release run, then claimed exactly once before target builds.
func TestPublishUsesTheSharedExactCommitPreflightBeforeAnythingIsPublished(t *testing.T) {
	const rel = ".github/workflows/publish.yml"
	wf := parseWorkflow(t, rel)
	gate, ok := wf.Jobs["publisher-preflight"]
	if !ok {
		t.Fatalf("%s lost its trusted preflight", rel)
	}
	if wf.Permissions == nil || len(wf.Permissions) != 0 {
		t.Fatal("publisher must start with no permissions")
	}
	checkout, preflight := -1, -1
	for i, s := range gate.Steps {
		if strings.HasPrefix(s.Uses, "actions/checkout@") {
			checkout = i
			if s.With["ref"] != "${{ github.sha }}" || s.With["persist-credentials"] != "false" {
				t.Errorf("preflight checkout is not trusted/unpersisted: %v", s.With)
			}
		}
		if strings.Contains(s.Run, "tools/release-wiring/preflight.sh") {
			preflight = i
			if s.Env["RELEASE_SHA"] != "${{ inputs.sha }}" || s.Env["WORKFLOW_SHA"] != "${{ github.sha }}" {
				t.Errorf("trusted/exact source identity not supplied: %v", s.Env)
			}
			for _, call := range []string{"ensure-release-present.sh", "verify-publisher-provenance"} {
				if strings.Index(s.Run, call) < strings.Index(s.Run, "preflight.sh") {
					t.Errorf("%s is not after exact CI/tag proof", call)
				}
			}
		}
	}
	if checkout < 0 || preflight < checkout {
		t.Fatal("trusted checkout must precede exact preflight")
	}
	if gate.Permissions["contents"] != "read" || gate.Permissions["actions"] != "read" {
		t.Errorf("preflight does not have readonly source/Actions scope: %v", gate.Permissions)
	}
	claim, ok := wf.Jobs["claim-publication"]
	if !ok || !contains(needs(claim.Needs), "publisher-preflight") {
		t.Fatal("create-only claim bypasses eligibility")
	}
	found := false
	for _, s := range claim.Steps {
		if strings.Contains(s.Run, "go run ./tools/release-wiring claim-publication") {
			found = true
			claimAt := strings.Index(s.Run, "go run ./tools/release-wiring claim-publication")
			for _, call := range []string{"tools/release-wiring/preflight.sh", "verify-publisher-provenance", "check-version-order"} {
				if at := strings.Index(s.Run, call); at < 0 || at >= claimAt {
					t.Errorf("claim precedes %s", call)
				}
			}
			for _, key := range []string{"RELEASE_SHA", "REQUEST_RUN_ID", "RELEASE_RUN_ID", "GITHUB_RUN_ATTEMPT"} {
				if s.Env[key] == "" {
					t.Errorf("claim lost exact provenance %s", key)
				}
			}
		}
	}
	if !found || claim.Permissions["contents"] != "write" {
		t.Fatal("atomic claim call/write scope removed")
	}
	var reaches func(string, string, map[string]bool) bool
	reaches = func(job, target string, seen map[string]bool) bool {
		if job == target {
			return true
		}
		if seen[job] {
			return false
		}
		seen[job] = true
		for _, n := range needs(wf.Jobs[job].Needs) {
			if reaches(n, target, seen) {
				return true
			}
		}
		return false
	}
	for id, job := range wf.Jobs {
		if !reaches(id, "publisher-preflight", map[string]bool{}) {
			t.Errorf("%s bypasses exact preflight", id)
		}
		write := false
		for _, scope := range job.Permissions {
			write = write || scope == "write"
		}
		for _, s := range job.Steps {
			if strings.HasPrefix(s.Uses, "actions/checkout@") {
				if s.With["persist-credentials"] != "false" {
					t.Errorf("%s persists checkout token", id)
				}
				if write && s.With["ref"] != "${{ github.sha }}" {
					t.Errorf("write/OIDC job %s checks out target source", id)
				}
			}
			if strings.Contains(s.Run, "go run ./tools/build-wheels") || strings.Contains(s.Run, "nix build") {
				if write || job.Permissions["contents"] != "read" {
					t.Errorf("target build %s has publication authority: %v", id, job.Permissions)
				}
				if !reaches(id, "claim-publication", map[string]bool{}) {
					t.Errorf("target build %s bypasses create-only claim", id)
				}
				for _, v := range s.Env {
					if strings.Contains(v, "secrets.") || strings.Contains(v, "github.token") {
						t.Errorf("target build %s receives publication credential", id)
					}
				}
			}
		}
	}
	for _, id := range []string{"claim-publication", "publish-wheels", "publish-image-cache", "push-builder-image", "publish-builder-index"} {
		job := wf.Jobs[id]
		if len(job.Steps) == 0 || job.Steps[0].Uses != "" || job.Steps[0].Env["GITHUB_RUN_ATTEMPT"] != "${{ github.run_attempt }}" || !strings.Contains(job.Steps[0].Run, `if [ "$GITHUB_RUN_ATTEMPT" != 1 ]; then`) || !strings.Contains(job.Steps[0].Run, "exit 1") {
			t.Errorf("%s does not independently refuse a failed-job rerun before credentials/actions", id)
		}
	}
	for _, id := range []string{"build-wheels", "build-image-cache", "build-builder-image"} {
		if !contains(needs(wf.Jobs[id].Needs), "claim-publication") {
			t.Errorf("%s lost direct claim dependency", id)
		}
	}
	if !contains(needs(wf.Jobs["publish-wheels"].Needs), "build-wheels") {
		t.Fatal("wheel publishing bypasses readonly build")
	}
	validation, upload := -1, -1
	for i, s := range wf.Jobs["publish-wheels"].Steps {
		if strings.Contains(s.Run, "validate-wheels") {
			validation = i
		}
		if strings.Contains(s.Run, "uv publish") {
			upload = i
		}
	}
	if validation < 0 || upload <= validation {
		t.Fatal("direct PyPI upload bypasses trusted inert wheel validation")
	}
}

// The pin/changelog checks execute only in readonly target preparation. The
// writer consumes validated inert files and runs neither target code nor hooks.
func TestReleasePreflightsBeforeGoReleaserAndHomebrew(t *testing.T) {
	const rel = ".github/workflows/release.yml"
	wf := parseWorkflow(t, rel)
	gate, ok := wf.Jobs["eligibility"]
	if !ok {
		t.Fatal("Release lost eligibility job")
	}
	if wf.Permissions == nil || len(wf.Permissions) != 0 {
		t.Fatal("Release must start with no permissions")
	}
	setup, check := -1, -1
	for i, s := range gate.Steps {
		if strings.HasPrefix(s.Uses, "actions/setup-go@") {
			setup = i
		}
		if strings.Contains(s.Run, "tools/release-wiring/preflight.sh") {
			check = i
			for _, call := range []string{"verify-release-request", "ensure-release-absent.sh", "ensure-release-present.sh"} {
				if at := strings.Index(s.Run, call); at < strings.Index(s.Run, "preflight.sh") {
					t.Errorf("eligibility %s bypasses preflight", call)
				}
			}
		}
	}
	if setup < 0 || check < setup {
		t.Fatal("Release preflight does not follow trusted Go setup")
	}
	prep := wf.Jobs["prepare-target"]
	if !contains(needs(prep.Needs), "eligibility") {
		t.Fatal("target source can run before eligibility")
	}
	pinCaller := false
	for _, s := range prep.Steps {
		pinCaller = pinCaller || strings.Contains(s.Run, "tools/release-wiring/validate-target.sh")
	}
	if !pinCaller || !strings.Contains(repoFile(t, "tools/release-wiring/validate-target.sh"), "if ! (cd \"$TARGET_ROOT\" && env -u GITHUB_TOKEN -u GH_TOKEN -u HOMEBREW_TAP_TOKEN -u CACHIX_AUTH_TOKEN go run ./tools/pack-binaries check") {
		t.Fatal("readonly pin refusal call/target credential stripping removed")
	}
	goreleaser := wf.Jobs["goreleaser-prepare"]
	if !contains(needs(goreleaser.Needs), "eligibility") || !contains(needs(goreleaser.Needs), "prepare-target") || goreleaser.If != "inputs.mode == 'publish'" {
		t.Fatal("GoReleaser bypasses target pins or runs for backfill")
	}
	cmp, run := -1, -1
	for i, s := range goreleaser.Steps {
		if strings.Contains(s.Run, "cmp --silent target/.goreleaser.yaml .goreleaser.yaml") {
			cmp = i
		}
		if strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action@") {
			run = i
			if s.With["version"] != "v2.18.2" || !strings.Contains(s.With["args"], "--skip=publish") || s.With["workdir"] != "target" {
				t.Errorf("GoReleaser is not pinned readonly preparation: %v", s.With)
			}
		}
	}
	if cmp < 0 || run <= cmp {
		t.Fatal("target GoReleaser config can bypass trusted comparison")
	}
	writer := wf.Jobs["publish-release"]
	if len(writer.Steps) == 0 || writer.Steps[0].Uses != "" || writer.Steps[0].Env["GITHUB_RUN_ATTEMPT"] != "${{ github.run_attempt }}" || !strings.Contains(writer.Steps[0].Run, `if [ "$GITHUB_RUN_ATTEMPT" != 1 ]; then`) || !strings.Contains(writer.Steps[0].Run, "exit 1") {
		t.Fatal("normal release mutation job does not independently refuse reruns before credential actions")
	}
	if !contains(needs(writer.Needs), "eligibility") || !contains(needs(writer.Needs), "goreleaser-prepare") || writer.Permissions["contents"] != "write" || writer.If != "inputs.mode == 'publish'" {
		t.Fatal("inert writer bypasses prepared inputs/eligibility")
	}
	upload, brew := -1, -1
	for _, s := range writer.Steps {
		if strings.Contains(s.Run, "tools/release-wiring/publish-release.sh") {
			upload = strings.Index(s.Run, "tools/release-wiring/publish-release.sh")
			brew = strings.Index(s.Run, "tools/release-wiring/update-homebrew.sh")
		}
	}
	if upload < 0 || brew <= upload {
		t.Fatal("normal formula update bypasses successful inert release upload")
	}
	uploadSource := repoFile(t, "tools/release-wiring/publish-release.sh")
	validation := strings.Index(uploadSource, "go run ./tools/release-wiring validate-assets")
	absent := strings.Index(uploadSource, `RELEASE_TAG="$tag" tools/release-wiring/ensure-release-absent.sh`)
	mutation := strings.Index(uploadSource, "gh api --method POST")
	if validation < 0 || absent <= validation || mutation <= absent {
		t.Fatal("inert writer bypasses complete validation or existing-release refusal")
	}
	backfill := wf.Jobs["homebrew-only"]
	if !contains(needs(backfill.Needs), "eligibility") || !contains(needs(backfill.Needs), "prepare-target") || backfill.If != "inputs.mode == 'homebrew-only'" {
		t.Fatal("backfill bypasses eligibility/pins or overlaps normal publication")
	}
	found := false
	for _, s := range backfill.Steps {
		if strings.Contains(s.Run, "update-homebrew.sh") {
			found = true
			for _, call := range []string{"tools/release-wiring/preflight.sh", "ensure-release-present.sh"} {
				if at := strings.Index(s.Run, call); at < 0 || at >= strings.Index(s.Run, "update-homebrew.sh") {
					t.Errorf("backfill bypasses %s", call)
				}
			}
		}
	}
	if !found {
		t.Fatal("retained main Homebrew-only caller removed")
	}
	for id, job := range wf.Jobs {
		write := false
		for _, scope := range job.Permissions {
			write = write || scope == "write"
		}
		if id == "eligibility" || id == "prepare-target" || id == "goreleaser-prepare" {
			if write || job.Permissions["contents"] != "read" {
				t.Errorf("target/gate job %s has write/OIDC authority %v", id, job.Permissions)
			}
		}
		for _, s := range job.Steps {
			if strings.HasPrefix(s.Uses, "actions/checkout@") {
				if s.With["persist-credentials"] != "false" {
					t.Errorf("%s persists credentials", id)
				}
				if write && s.With["ref"] != "${{ github.sha }}" {
					t.Errorf("write job %s checks out target source", id)
				}
			}
			if !write && (id == "prepare-target" || id == "goreleaser-prepare") {
				for _, v := range s.Env {
					if strings.Contains(v, "secrets.") || strings.Contains(v, "github.token") {
						t.Errorf("target source job %s receives publication credential", id)
					}
				}
			}
			if write && (strings.Contains(s.Run, "cd target") || strings.Contains(s.With["workdir"], "target") || strings.HasPrefix(s.Uses, "goreleaser/")) {
				t.Errorf("writer %s executes target source/hooks", id)
			}
		}
	}
}

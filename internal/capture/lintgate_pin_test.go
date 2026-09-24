package capture

// lintgate_pin_test.go is the CROSS-LANGUAGE call-site pin for this repo's cross-GOOS
// lint gate.
//
// internal/capture is one of the two packages that made the gate necessary. clone_other.go
// is a `//go:build !linux` refusal, and under GOOS=darwin it is the cloneFile that
// materialize's reflink arm calls — so everything the analyzers say about that arm on a
// Mac is said about a DIFFERENT function than the one they read on Linux. Nothing in Go's
// toolchain looks past a build constraint it did not select, so for as long as the gate
// ran one GOOS, every non-linux half in the tree was unanalyzed and the findings waited for
// whoever built from source on macOS first (GitHub issue #42).
//
// The gate itself is a `just` recipe: no compiler, no import graph, nothing that fails when
// it is deleted. AGENTS.md names that shape — "a test that pins the CALLEE while the CALL
// SITE is unpinned is not a test" — so the recipe is pinned from here, and the SURVEY below
// pins the thing the recipe is for: that no file in the tree is left outside every pass.

import (
	"go/build"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"
)

// lintedGOOS is the set of GOOS values the `lint` recipe analyzes, and the set this test
// evaluates every build constraint against. It is a copy of a decision made in the
// Justfile; TestLintRecipeRunsStaticcheckForEveryLintedGOOS is what keeps the copy honest.
var lintedGOOS = []string{"linux", "darwin"}

// gateGOARCH is every architecture a host running the gate can have. The `lint` recipe
// passes no GOARCH, so each host lints at its own, and a file an amd64 host analyzes can be
// invisible to an arm64 one (CI's check-macos runner, an Apple Silicon Mac, arm64 Linux).
var gateGOARCH = []string{"amd64", "arm64"}

// unanalyzedFiles names every file no pass in lintedGOOS selects, with the reason. An entry
// here is a DECISION to leave a file unlinted — the list exists so that "outside the gate"
// cannot be an oversight, the same way shipSetExemptCmds does it for the ship set.
//
// Keys are slash-separated paths relative to the repo root.
var unanalyzedFiles = map[string]string{
	"internal/serialdaemon/serial_other.go": "`!linux && !darwin` is the completeness arm of " +
		"serialdaemon's constraint set, not a target: the tree does not compile under " +
		"GOOS=windows at all, so a third lint pass would report a broken build rather than a " +
		"finding. Adding a GOOS that DOES compile is the way to retire this entry.",
}

// repoRootDir locates the checkout from this test file's compile-time path.
//
// It FAILS rather than skips when the tree is not readable, for the reason
// internal/entrypoint's repoRoot does: a skip would turn the silent non-coverage this test
// exists to catch into silent non-coverage one level up.
func repoRootDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path — cannot locate the repo root")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	if _, err := os.Stat(filepath.Join(root, "flake.nix")); err != nil {
		t.Fatalf("no flake.nix at the resolved repo root %s: %v", root, err)
	}
	return root
}

// justRecipeRe captures a named recipe's body: the dependency line, then every indented or
// blank line until the next line that starts in column zero.
func justRecipeRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `:[^\n]*\n((?:[ \t][^\n]*\n|\n)*)`)
}

func justRecipeBody(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRootDir(t), "Justfile"))
	if err != nil {
		t.Fatalf("read Justfile: %v", err)
	}
	m := justRecipeRe(name).FindSubmatch(body)
	if m == nil {
		t.Fatalf("the Justfile no longer declares a %q recipe. The cross-GOOS lint gate "+
			"lives in it; if the recipe was renamed, move this pin with it rather than "+
			"deleting it.", name)
	}
	return string(m[1])
}

// TestLintRecipeRunsStaticcheckForEveryLintedGOOS is the call-site pin.
//
// Delete the `GOOS=darwin staticcheck` line and this test fails — which is the whole point:
// nothing else in the repo notices, because no Go code calls a `just` recipe. `go vet` is
// pinned alongside it because it is analyzed per-GOOS in exactly the same way and would go
// blind in exactly the same silence.
func TestLintRecipeRunsStaticcheckForEveryLintedGOOS(t *testing.T) {
	body := justRecipeBody(t, "lint")

	for _, goos := range lintedGOOS {
		for _, tool := range []string{"staticcheck", "go vet"} {
			want := regexp.MustCompile(`(?m)^\s*GOOS=` + goos + `\s+` + regexp.QuoteMeta(tool) + `\b`)
			if !want.Match([]byte(body)) {
				t.Errorf("the `lint` recipe no longer runs `%s` under GOOS=%s.\n"+
					"A build-tagged file is type-checked and analyzed only under the GOOS "+
					"that selects it, so dropping a pass makes every file behind that "+
					"constraint unlinted — silently, with the gate still green. That is "+
					"GitHub issue #42, which reached a macOS contributor before it reached "+
					"CI. Restore the pass, or remove %q from lintedGOOS and say why in the "+
					"Justfile.", tool, goos, goos)
			}
		}
	}

	// A bare invocation means "whatever GOOS this machine happens to be", which is the
	// drift the explicit passes exist to remove: on a Mac it silently retargets the pass
	// that is supposed to be the linux one.
	for _, tool := range []string{"staticcheck", "go vet"} {
		bare := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(tool) + `\b`)
		if bare.Match([]byte(body)) {
			t.Errorf("the `lint` recipe runs a bare `%s` again. Every pass must name its "+
				"GOOS, or `just lint` analyzes a different set of files depending on whose "+
				"laptop it runs on.", tool)
		}
	}
}

// TestTheHookAndCIReachTheSameLintPasses pins the OTHER half of the call site. `check-go` in
// ci.yml and the pre-commit hook both run `just check-ci` -> `lint-ci`, so a `lint-ci` that
// restates the commands instead of depending on `lint` is a second list to keep in step —
// and a gate applied on one path and not the other is the failure mode this whole file is
// about, one level up.
func TestTheHookAndCIReachTheSameLintPasses(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRootDir(t), "Justfile"))
	if err != nil {
		t.Fatalf("read Justfile: %v", err)
	}
	dep := regexp.MustCompile(`(?m)^lint-ci:([^\n]*)$`)
	m := dep.FindSubmatch(body)
	if m == nil {
		t.Fatal("the Justfile no longer declares a `lint-ci` recipe — `just check-ci`, the " +
			"pre-commit hook and ci.yml's check-go job all reach the lint passes through it")
	}
	if !strings.Contains(string(m[1]), "lint") {
		t.Errorf("`lint-ci` no longer depends on `lint` (its dependency line is %q), so the "+
			"hook and CI run whatever `lint-ci` restates rather than the passes `lint` "+
			"declares. Two hand-kept lists is how a gate gets applied on one path and not "+
			"the other.", strings.TrimSpace(string(m[1])))
	}
}

// TestEveryGoFileIsAnalyzedBySomeLintPass is the survey half: the recipe pin above proves
// the passes are RUN, this proves they are ENOUGH.
//
// It asks the same question the toolchain does — go/build's own MatchFile, so filename
// suffixes (`foo_darwin.go`), `//go:build` expressions and pseudo-tags like `unix` are all
// honoured exactly as `go build` honours them — for each GOOS in lintedGOOS. A file no
// context selects is a file every analyzer this repo pays for skips, forever, with every
// gate green. Add `//go:build freebsd` tomorrow and this fails until someone decides
// whether to widen the gate or to declare the file unanalyzed.
//
// GOARCH: the `lint` recipe leaves it at the HOST's, and the gate runs on hosts of both
// architectures in gateGOARCH (CI's Linux amd64 and macOS arm64 runners, and developers'
// machines of either kind). So a file must be selected at EVERY one of them, not just at
// the architecture this test happens to run on: a `_linux_amd64.go` file passed an amd64
// gate and then failed check-macos on arm64 (2026-09-24), because on arm64 no lint pass
// ever reads it.
func TestEveryGoFileIsAnalyzedBySomeLintPass(t *testing.T) {
	root := repoRootDir(t)

	contexts := map[string][]build.Context{}
	for _, goarch := range gateGOARCH {
		for _, goos := range lintedGOOS {
			ctx := build.Default
			ctx.GOOS = goos
			ctx.GOARCH = goarch
			// Cross-compilation disables cgo, and so does every pass but the host's.
			ctx.CgoEnabled = false
			contexts[goarch] = append(contexts[goarch], ctx)
		}
	}

	seen := map[string]bool{}
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			seen[rel] = true

			why, exempt := unanalyzedFiles[rel]
			for _, goarch := range gateGOARCH {
				selected := false
				for _, ctx := range contexts[goarch] {
					ok, merr := ctx.MatchFile(filepath.Dir(path), d.Name())
					if merr != nil {
						t.Errorf("%s: cannot evaluate its build constraint for %s/%s: %v",
							rel, ctx.GOOS, goarch, merr)
						return nil
					}
					if ok {
						selected = true
						if exempt {
							t.Errorf("%s is declared unanalyzed (%q) but %s/%s DOES select "+
								"it — drop the unanalyzedFiles entry.", rel, why, ctx.GOOS, goarch)
						}
						break
					}
				}
				if !selected && !exempt {
					t.Errorf("%s is selected by no GOOS in %v at GOARCH=%s, so `just lint` on "+
						"an %s host never reads it: go vet, staticcheck and the type-checker all "+
						"stop at the build constraint. Widen lintedGOOS (and the Justfile's "+
						"`lint` recipe with it), drop the architecture constraint, or add the "+
						"file to unanalyzedFiles with the reason.",
						rel, lintedGOOS, goarch, goarch)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s/: %v", top, err)
		}
	}

	// An exemption for a file that no longer exists is a stale claim about the tree, and
	// the next reader has no way to tell it from a live one.
	for rel := range unanalyzedFiles {
		if !seen[rel] {
			t.Errorf("unanalyzedFiles names %s, which is not in the tree — remove the "+
				"entry", rel)
		}
	}
}

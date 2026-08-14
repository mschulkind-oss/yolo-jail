package loopholes

// censusconvergence_test.go is the STRUCTURAL half of docs/design/loophole-packaging.md
// §5.1's requirement: the pack-aware, lock-gated loophole set is ONE constructed value,
// "not seven independent DiscoverOptions assemblies. Assert the convergence in a test."
//
// The behavioural half is convergence_test.go — that a pack module is discovered, gated and
// visible from every surface. This half is the one that keeps it true: a new consumer added
// next month reaches for `loopholes.Discover(loopholes.DiscoverOptions{IncludeBundled:
// true, LoopholesConfig: …})`, because that literal is what all six call sites looked like,
// and it would silently see NO pack loopholes while every other surface sees them. Six
// hand-built struct literals is six chances to disagree about what this machine has, and two
// of the surfaces EXECUTE host code — so a disagreement there is not cosmetic drift, it is a
// read-only preflight running a daemon's self-check.
//
// It reads the SOURCE TREE rather than a symbol table because the property is about how
// callers are WRITTEN, which no runtime assertion can observe.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// convergenceExemptions are the files allowed to name Discover/DiscoverOptions directly,
// each with the reason. Adding a file here is a deliberate act, which is the point.
var convergenceExemptions = map[string]string{
	// The definitions themselves, and NewSet/NewHostSet — the converged constructors.
	"internal/loopholes/discover.go": "defines Discover, DiscoverOptions and the converged constructors",
	// Census site 6. It cannot use NewHostSet: config.LoopholeResolver is an interface the
	// config package owns, and the Resolver carries its own Root/IncludeBundled knobs
	// (config-dump and yolo check construct it with an explicit root). It reads the same
	// recorded PackModules(), which is where the convergence actually lands.
	"internal/loopholes/resolver.go": "config.LoopholeResolver's own knobs; reads the same PackModules()",
}

// TestEveryDiscoverCallSiteIsConverged walks every non-test .go file in the module and fails
// on any un-exempt file that builds its own DiscoverOptions or calls Discover directly.
func TestEveryDiscoverCallSiteIsConverged(t *testing.T) {
	root := repoRootDir(t)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil // an unreadable subtree is not this test's business
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", ".git", "dist-go", "node_modules", "bin":
				return filepath.SkipDir
			// .claude holds agent WORKTREES — other checkouts of this same repo,
			// at other commits. Walking them reports their copies of these files as
			// offenders here, which is both wrong (they are not this tree's source)
			// and unfixable from this tree. Without this the test fails on any
			// machine that has ever run a worktree-isolated agent, and the failure
			// text looks exactly like a real convergence regression.
			case ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if _, exempt := convergenceExemptions[rel]; exempt {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(body)
		for _, bad := range []string{"DiscoverOptions{", "loopholes.Discover(", "= Discover("} {
			if strings.Contains(text, bad) {
				offenders = append(offenders, rel+": "+bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("these files assemble their own loophole discovery instead of going through the "+
			"ONE constructed value (loopholes.NewHostSet):\n  %s\n\n"+
			"That is the seven-surface divergence docs/design/loophole-packaging.md §5.1 exists to "+
			"close. A hand-built DiscoverOptions sees no PACK loopholes (the field defaults empty) "+
			"and no bundled ones (IncludeBundled's zero value is false), so the new surface "+
			"disagrees with every other one about what this machine has — and if it runs a "+
			"doctor_cmd, it does so with the origin gate nowhere in its call graph.\n\n"+
			"Use loopholes.NewHostSet(cfgMap(cfg, \"loopholes\")) and one of its views "+
			"(All/Enabled/Active/Lookup). If a new surface genuinely needs different inputs, add it "+
			"to convergenceExemptions WITH THE REASON.", strings.Join(offenders, "\n  "))
	}
}

// The census is SEVEN surfaces, and this counts them so the number in the docs and the
// number in the tree cannot drift apart silently. It counts the CONVERGED constructor's
// callers plus the two exempt sites plus the walker.
//
// Re-derived 2026-08-14 against HEAD (not taken from the doc):
//
//	1  internal/cli/run/prepare.go            the briefing
//	2  internal/cli/run/assemble_parts.go     brokerLoopholeActive
//	3  internal/cli/run/assemble_parts.go     loopholesRuntimeArgs (container argv)
//	4  internal/cli/run/loopholesruntime.go   startLoopholes (the host daemon spawn)
//	5  internal/loopholes/loopholescmd.go     `yolo loopholes list` / `status`
//	6  internal/loopholes/resolver.go         config.LoopholeResolver.Known()
//	7  internal/loopholes/discover.go         ValidateLoopholes, via internal/cli/check
func TestCensusIsSevenSurfaces(t *testing.T) {
	root := repoRootDir(t)
	// The five NewHostSet consumers (sites 1-5) plus the two that read PackModules()
	// directly (6, and 7's walker). Listed by FILE, since two sites live in one file.
	wantFiles := map[string][]string{
		"internal/cli/run/prepare.go":          {"NewHostSet("},
		"internal/cli/run/assemble_parts.go":   {"NewHostSet("},
		"internal/cli/run/loopholesruntime.go": {"NewHostSet("},
		"internal/loopholes/loopholescmd.go":   {"NewHostSet("},
		"internal/loopholes/resolver.go":       {"PackModules()"},
		"internal/loopholes/discover.go":       {"PackModules()"},
	}
	for rel, wants := range wantFiles {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v — a census surface moved or was deleted; re-derive the census and "+
				"update this test and §5.1 together", rel, err)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s no longer contains %q — it was one of the seven loophole discovery "+
					"surfaces, and a surface that stops reading the converged set silently stops "+
					"seeing pack loopholes", rel, want)
			}
		}
	}
	// assemble_parts.go carries TWO of the seven (brokerLoopholeActive and
	// loopholesRuntimeArgs), which is why the file count is six and the census is seven.
	body, err := os.ReadFile(filepath.Join(root, "internal/cli/run/assemble_parts.go"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), "NewHostSet("); n != 2 {
		t.Errorf("assemble_parts.go calls NewHostSet %d times, want 2 (brokerLoopholeActive and "+
			"loopholesRuntimeArgs are two distinct census surfaces in one file — that is why the "+
			"census is SEVEN over six files plus the walker)", n)
	}
}

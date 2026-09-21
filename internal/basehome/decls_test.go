// decls_test.go is an EXTERNAL test package on purpose: it pins the derive against lists
// that live in packages internal/basehome must not import in production — internal/prune
// (which drags the image and container-builder trees onto the launch path) — so the edge
// exists only in the test binary.
package basehome_test

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/basehome"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestShippedDeclsAreNotEmpty is the anti-no-op guard, and it is the single most
// load-bearing test here.
//
// packload.Embedded() returns an EMPTY set on any materialization problem — conservative
// for a reservation list, and for a WALK it means zero roots, zero candidates and a report
// that is indistinguishable from a clean host. The failure mode is a feature that ships
// switched off with the whole suite green. It is also one blank import away
// (internal/packreg wires the embedded FS into packload; without it every list below is
// nil).
func TestShippedDeclsAreNotEmpty(t *testing.T) {
	d := basehome.ShippedDecls()
	if len(d.StateDirs) == 0 {
		t.Fatal("no state dirs: detection has no roots and silently reports nothing. " +
			"The usual cause is internal/packreg not being imported (see decls.go).")
	}
	if len(d.Problems) != 0 {
		t.Fatalf("ShippedDecls reported problems %v; the shipped manifests must all read cleanly", d.Problems)
	}
	for _, want := range []string{".claude", ".codex", ".copilot", ".gemini", ".pi"} {
		if !has(d.StateDirs, want) {
			t.Errorf("StateDirs %v is missing %s", d.StateDirs, want)
		}
	}
	// The third is pi's extension package STORE rather than a credential dir, and it needs
	// the exclusion for the same structural reason: the sweep walks a pack's state dirs, and
	// `.pi-shared-npm` is machine-scope state whose bytes belong to every workspace at once.
	// A `node_modules` tree proposed for archiving would be the sweep offering to break every
	// jail's extensions at once.
	for _, want := range []string{
		".claude-shared-credentials", ".gemini-shared-credentials", ".pi-shared-npm",
	} {
		if !has(d.SharedDirs, want) {
			t.Errorf("SharedDirs %v is missing %s — its contents would be swept", d.SharedDirs, want)
		}
	}
}

// TestCredentialFilesComeFromTheSharedCredentialsHookOnly is the ONE-TOKEN GOTCHA.
//
// HookContributions returns BOTH hook kinds in one slice. Drop the `h.Name !=
// hookSharedCredentials` filter and `.claude/history.jsonl` — the per_jail_history
// declaration, and the single purest runtime artifact in the base home — enters the
// CREDENTIAL set, so the quarantine preserves exactly what it exists to evict while
// reporting success. No compile error, no symptom.
func TestCredentialFilesComeFromTheSharedCredentialsHookOnly(t *testing.T) {
	d := basehome.ShippedDecls()
	for _, want := range []string{
		".claude/.credentials.json",
		".gemini/antigravity-cli/antigravity-oauth-token",
	} {
		if !has(d.CredentialFiles, want) {
			t.Errorf("CredentialFiles %v is missing the declared %s", d.CredentialFiles, want)
		}
	}
	if has(d.CredentialFiles, ".claude/history.jsonl") {
		t.Fatal("history.jsonl is in the CREDENTIAL set: the hook-NAME filter is gone, " +
			"so per_jail_history's declaration is being read as a credential")
	}
	if got := d.Classify(".claude/history.jsonl"); got != basehome.Runtime {
		t.Fatalf("Classify(.claude/history.jsonl) = %s, want RUNTIME — the per_jail_history "+
			"hook is a POSITIVE runtime assertion", got)
	}
}

// TestCredentialHookNameIsAKnownHook pins the spelled hook name against the closed set
// packdecl publishes, which is the same list entrypoint's own constants are pinned
// against (packdecl.TestHookSetsAgree). A typo here disables the derive silently.
func TestCredentialHookNameIsAKnownHook(t *testing.T) {
	d := basehome.ShippedDecls()
	if len(d.CredentialFiles) == 0 {
		t.Fatal("no credential files derived, so the hook name this test is about is not being matched")
	}
	if !has(packdecl.KnownHooks, "shared_credentials") {
		t.Fatalf("packdecl.KnownHooks = %v no longer contains shared_credentials; the derive in "+
			"decls.go spells it as a literal and must be updated with it", packdecl.KnownHooks)
	}
}

// TestEveryShippedContentDestinationSurvives is the Inferred trap, measured.
//
// Over the shipped set packload.ResolveDestinations returns ZERO Inferred contributions —
// every agent pack declares its own `into` — so a derive written against Destinations.
// Inferred (whose doc says "what was ADDED, for the report") compiles, passes any test
// built from it, and classifies ALL of the shipped destinations as RUNTIME. That archives
// .claude/CLAUDE.md and .claude/skills. This test reads the destinations the same way the
// design does and asserts none of them is a candidate.
func TestEveryShippedContentDestinationSurvives(t *testing.T) {
	d := basehome.ShippedDecls()
	var checked int
	for _, p := range packload.Embedded() {
		for _, c := range p.Decl.Contributions() {
			switch c.Kind {
			case packdecl.KindSkills, packdecl.KindBriefing, packdecl.KindFiles:
			default:
				continue
			}
			if c.Into == "" {
				continue
			}
			rel, ok := basehome.HomeRel(c.Into)
			if !ok {
				t.Errorf("destination %q is not home-relative", c.Into)
				continue
			}
			checked++
			if got := d.Classify(rel); got != basehome.Content {
				t.Errorf("Classify(%s) = %s, want CONTENT (declared by pack %s)", rel, got, p.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no declared content destinations found; the assertion above is vacuous")
	}
}

// TestEveryShippedConfigSurfaceSurvives does the same for the config surfaces, through
// the redirect §5.2 step 3 requires.
//
// The redirect exists for exactly ONE shipped surface — claude/config, `~/.claude.json`,
// which the base home keeps at `.claude/claude.json`. Skip the clause and the file holding
// oauthAccount classifies RUNTIME and is archived: a forced re-login on every workspace,
// reached through the one clause most likely to be dropped as an edge case.
func TestEveryShippedConfigSurfaceSurvives(t *testing.T) {
	d := basehome.ShippedDecls()
	redirect := map[string]string{}
	for _, r := range paths.HomeFileRedirects() {
		redirect[r.Name] = r.Target
	}
	var checked, redirected int
	for _, p := range packload.Embedded() {
		surfaces, problems := p.Surfaces()
		if len(problems) > 0 {
			t.Errorf("pack %s surfaces: %v", p.Name, problems)
			continue
		}
		for _, s := range surfaces {
			rel, ok := basehome.HomeRel(s.Path)
			if !ok {
				continue
			}
			if target, isRedirected := redirect[rel]; isRedirected {
				rel = target
				redirected++
			}
			checked++
			if d.Classify(rel).Candidate() {
				t.Errorf("Classify(%s) is a candidate; %s/%s declares it as a config surface",
					rel, s.Agent, s.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no config surfaces found; the assertion above is vacuous")
	}
	if redirected == 0 {
		t.Fatal("no surface went through a redirect, so the redirect clause is untested here — " +
			"claude/config (~/.claude.json) is the one that needs it")
	}
}

// TestEveryShadowedHomeDirIsExcluded pins the drift between the unknown-top-level sweep's
// exclusion set and prune's shadowed-home registry. §8 rules the caches out twice ("not a
// disk reclaimer", "not PruneShadowedHome"), and the sweep would otherwise propose
// archiving the npm cache — which EnsureGlobalStorage does not create, so
// paths.BaseHomeCoreDirs cannot carry it.
func TestEveryShadowedHomeDirIsExcluded(t *testing.T) {
	d := basehome.ShippedDecls()
	for _, dir := range prune.ShadowedHomePaths {
		if !has(d.NonPackDirs, dir) {
			t.Errorf("NonPackDirs %v is missing prune.ShadowedHomePaths entry %q", d.NonPackDirs, dir)
		}
	}
}

// TestEveryCoreProvisionedDirIsExcluded pins the other half of the same seam: what
// EnsureGlobalStorage creates in the base home is core's, not a retired pack's, and §8
// puts it out of scope. paths.BaseHomeCoreDirs is the shared authority, which is why it
// was extracted from the inline list inside EnsureGlobalStorage.
func TestEveryCoreProvisionedDirIsExcluded(t *testing.T) {
	d := basehome.ShippedDecls()
	if len(paths.BaseHomeCoreDirs()) == 0 {
		t.Fatal("paths.BaseHomeCoreDirs is empty")
	}
	for _, dir := range paths.BaseHomeCoreDirs() {
		if !has(d.NonPackDirs, dir) {
			t.Errorf("NonPackDirs %v is missing core-provisioned %q", d.NonPackDirs, dir)
		}
	}
}

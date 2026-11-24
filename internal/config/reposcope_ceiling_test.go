package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeScopedCeilingUser writes the user config a scoped ceiling reads: the broad allow.github table,
// then the repos tables as written.
func writeScopedCeilingUser(t *testing.T, broad, repos string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "packs = ['github']\n\n[allow.github]\nalways = ['read']\n" + broad + "\n" + repos
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A `[repos.<name>.allow.github]` table is the ceiling for the repository its repo line admits, on
// top of the broad `[allow.github]` (GW-D64): the broker asks for the identity it is serving, and a
// table for another identity covers nothing, though the alias is the same (`repos.jail`).
func TestAScopedCeilingCoversOnlyTheIdentityItsRepoLineAdmits(t *testing.T) {
	writeScopedCeilingUser(t, "on_request = ['create_pr']\n",
		"[repos.jail]\nrepo = 'github.com/o/r'\n\n[repos.jail.allow.github]\non_request = ['runs']\n")
	kinds, canonical, err := BrokeredGrantKindsForRepo("github", "github.com/o/r")
	if err != nil || !canonical || !slices.Contains(kinds, "runs") {
		t.Fatalf("o/r kinds %v, canonical %t, err %v: want runs from the scoped table", kinds, canonical, err)
	}
	kinds, _, err = BrokeredGrantKindsForRepo("github", "github.com/o/other")
	if err != nil || slices.Contains(kinds, "runs") {
		t.Fatalf("o/other kinds %v, err %v: the scoped table admits o/r only", kinds, err)
	}
}

// The scoped kinds add to the broad ones on each identity the table admits; a broad kind is never
// narrowed by a table for another identity.
func TestAScopedCeilingAddsToTheBroadOneOnItsIdentity(t *testing.T) {
	writeScopedCeilingUser(t, "on_request = ['create_pr']\n",
		"[repos.jail]\nrepo = 'github.com/o/r'\n\n[repos.jail.allow.github]\non_request = ['runs']\n")
	kinds, _, err := BrokeredGrantKindsForRepo("github", "github.com/o/r")
	if err != nil || !slices.Contains(kinds, "runs") || !slices.Contains(kinds, "create-pr") {
		t.Fatalf("o/r kinds %v, err %v: want both the broad create-pr and the scoped runs", kinds, err)
	}
	kinds, _, err = BrokeredGrantKindsForRepo("github", "github.com/o/other")
	if err != nil || !slices.Contains(kinds, "create-pr") || slices.Contains(kinds, "runs") {
		t.Fatalf("o/other kinds %v, err %v: want only the broad create-pr", kinds, err)
	}
}

// A pattern repo line admits by whole segment, as a grant's pattern does (GW-D80).
func TestAScopedPatternAdmitsItsOwnerOnly(t *testing.T) {
	writeScopedCeilingUser(t, "",
		"[repos.org]\nrepo = 'github.com/o/*'\n\n[repos.org.allow.github]\non_request = ['runs']\n")
	for _, c := range []struct {
		repo string
		want bool
	}{{"github.com/o/r", true}, {"github.com/o/anything", true}, {"github.com/p/r", false}} {
		kinds, _, err := BrokeredGrantKindsForRepo("github", c.repo)
		if err != nil || slices.Contains(kinds, "runs") != c.want {
			t.Fatalf("%s kinds %v, err %v: runs admitted %t, want %t", c.repo, kinds, err, !c.want, c.want)
		}
	}
}

// Only this source's table is read: another broker's table with a capability this broker does
// not declare does not fail the github call, and a table naming another host counts for nothing.
func TestAScopedCeilingIsReadPerSourceAndCountsOnlyItsHost(t *testing.T) {
	writeScopedCeilingUser(t, "",
		"[repos.jail]\nrepo = 'github.com/o/r'\n\n[repos.jail.allow.git]\non_request = ['no_such_capability']\n\n"+
			"[repos.jail.allow.github]\non_request = ['runs']\n")
	kinds, _, err := BrokeredGrantKindsForRepo("github", "github.com/o/r")
	if err != nil || !slices.Contains(kinds, "runs") {
		t.Fatalf("github kinds %v, err %v: git's table must not poison the github call", kinds, err)
	}
	writeScopedCeilingUser(t, "",
		"[repos.jail]\nrepo = 'gitlab.example/o/r'\n\n[repos.jail.allow.github]\non_request = ['runs']\n")
	kinds, _, err = BrokeredGrantKindsForRepo("github", "github.com/o/r")
	if err != nil || slices.Contains(kinds, "runs") {
		t.Fatalf("kinds %v, err %v: a table on another host admits nothing here", kinds, err)
	}
}

// A malformed table of this source fails closed: no kinds and an error naming the capability.
func TestAScopedCeilingFailsClosedOnItsOwnBadCapability(t *testing.T) {
	writeScopedCeilingUser(t, "on_request = ['create_pr']\n",
		"[repos.jail]\nrepo = 'github.com/o/r'\n\n[repos.jail.allow.github]\non_request = ['no_such_capability']\n")
	kinds, canonical, err := BrokeredGrantKindsForRepo("github", "github.com/o/r")
	if err == nil || !canonical || len(kinds) != 0 || !strings.Contains(err.Error(), "no_such_capability") {
		t.Fatalf("kinds %v, canonical %t, err %v: want no authority and an error naming the capability", kinds, canonical, err)
	}
}

// A repository name that is not concrete has no identity to admit, so no scoped kind is granted.
func TestAScopedCeilingNeedsAConcreteIdentity(t *testing.T) {
	writeScopedCeilingUser(t, "",
		"[repos.org]\nrepo = 'github.com/o/*'\n\n[repos.org.allow.github]\non_request = ['runs']\n")
	if kinds, _, _ := BrokeredGrantKindsForRepo("github", "github.com/o/*"); len(kinds) != 0 {
		t.Fatalf("a wildcard identity got kinds %v", kinds)
	}
}

// The gate refuses a capability list written directly under repos.<name>.allow: the lists belong
// to a broker's own table, [repos.<name>.allow.<broker>], as the broad lists belong to [allow.<broker>].
func TestAScopedCeilingIsRefusedDirectlyUnderAllow(t *testing.T) {
	user := repoScopeUserAll + "\n[repos.jail]\nrepo = \"github.com/o/r\"\n\n[repos.jail.allow]\non_request = [\"runs\"]\n"
	workspace := repoScopeWorkspace(t, user, repoScopeOrigin, repoScopeOriginLines, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal == nil || !strings.Contains(check.Fatal.Error(), "[allow.github]") {
		t.Fatalf("a list directly under repos.jail.allow: fatal %v, want a refusal naming [allow.github]", check.Fatal)
	}
}

// Scoped reads are retained as TOML policy metadata, not projected into native write grants.
func TestAScopedCeilingRetainsReadMetadata(t *testing.T) {
	for _, list := range []string{"always", "on_request"} {
		t.Run(list, func(t *testing.T) {
			user := repoScopeUserAll + "[repos.jail]\nrepo = \"github.com/acme/app\"\n[repos.jail.allow.github]\n" + list + " = [\"read\"]\n"
			workspace := repoScopeWorkspace(t, user, repoScopeOrigin, repoScopeOriginLines, "")
			check, _ := repoScopeCheck(t, workspace)
			if check.Fatal != nil {
				t.Fatalf("scoped read metadata refused: %v", check.Fatal)
			}
		})
	}
}

// Adding read metadata does not invent a native write kind or remove existing write ceilings.
func TestScopedReadMetadataLeavesNativeWriteKindsUnchanged(t *testing.T) {
	writeScopedCeilingUser(t, "on_request = ['create_pr']\n",
		"[repos.jail]\nrepo = 'github.com/o/r'\n\n[repos.jail.allow.github]\nalways = ['read']\non_request = ['runs']\n")
	kinds, canonical, err := BrokeredGrantKindsForRepo("github", "github.com/o/r")
	if err != nil || !canonical || !slices.Equal(kinds, []string{"create-pr", "runs"}) {
		t.Fatalf("kinds %v, canonical %t, err %v: scoped read changed the existing write kinds", kinds, canonical, err)
	}
}

// Design 3.2 and section 8: a narrower never on one repository removes that kind from the live
// answer at the next call, with no apply or relaunch. A never on another repository, and a narrower
// always that outranks a broader never, leave it alone.
func TestANarrowerNeverRemovesALiveKindOnItsRepositoryOnly(t *testing.T) {
	broad := "on_request = ['runs', 'create_pr']\n"
	table := func(repo, lists string) string {
		return "[repos.jail]\nrepo = 'github.com/" + repo + "'\n\n[repos.jail.allow.github]\n" + lists
	}
	for name, c := range map[string]struct {
		repos, repo string
		wantRuns    bool
		wantPR      bool
	}{
		"control, no table":                     {"", "o/r", true, true},
		"never runs on this repository":         {table("o/r", "never = ['runs']\n"), "o/r", false, true},
		"never runs on another repository":      {table("o/r", "never = ['runs']\n"), "o/other", true, true},
		"pattern never":                         {table("o/*", "never = ['runs']\n"), "o/r", false, true},
		"exact always outranks a pattern never": {table("o/*", "never = ['runs']\n") + "\n[repos.exact]\nrepo = 'github.com/o/r'\n\n[repos.exact.allow.github]\nalways = ['runs']\n", "o/r", true, true},
	} {
		t.Run(name, func(t *testing.T) {
			writeScopedCeilingUser(t, broad, c.repos)
			kinds, _, err := BrokeredGrantKindsForRepo("github", "github.com/"+c.repo)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(kinds, "runs") != c.wantRuns || slices.Contains(kinds, "create-pr") != c.wantPR {
				t.Fatalf("kinds %v, want runs=%v create-pr=%v", kinds, c.wantRuns, c.wantPR)
			}
		})
	}
}

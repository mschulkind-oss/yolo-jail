package cli

import (
	"strings"
	"testing"
)

// THE FETCH REMEDY IS OFFERED ONLY WHERE A FETCH REPAIRS THE PACK. A never-fetched git pack
// needs a fetch (NeedsInstall); a subpath absent at the fetched commit does not — no fetch
// creates a directory the commit lacks — so it is an address to fix, and the remedy that says
// "retries the fetch this apply already attempted" must not be printed for it.
func TestUnresolvedPackNeedsInstallOnlyForAFetchableMiss(t *testing.T) {
	repo := gitPackRepo(t)
	home := gitPackHome(t, "git+file://"+repo+"//tools/agent-pack?ref=main", "")
	installGitPack(t)

	selectPacksWith(t, home, `"claude",{"source":"git+file://`+repo+`//no/such/dir?ref=main","name":"gp"}`, "")
	_, unresolved := loadPromoteFold()
	if len(unresolved) != 1 || unresolved[0].Name != "gp" {
		t.Fatalf("unresolved = %+v, want gp", unresolved)
	}
	if u := unresolved[0]; u.NeedsInstall || !strings.Contains(u.Reason, "not found") {
		t.Errorf("a subpath missing at the commit was offered the fetch remedy: %+v", u)
	}
	for _, g := range unresolvedPackGroups(unresolved) {
		if strings.Contains(g.Remedy, "retries the fetch") {
			t.Errorf("remedy for a missing subpath offers a fetch: %q", g.Remedy)
		}
	}

	selectPacksWith(t, home, `"claude",{"source":"`+neverFetchedGitSource+`","name":"gp"}`, "")
	if _, unresolved := loadPromoteFold(); len(unresolved) != 1 || !unresolved[0].NeedsInstall {
		t.Errorf("a never-fetched pack was not offered the fetch remedy: %+v", unresolved)
	}
}

package packload

// duplicateloads_ref_test.go pins two readings of the duplicate-load lint (duplicateloads.go,
// docs/design/patched-extensions.md PPX-D34) that its first tests left open: a remote entry whose
// ref holds a slash still has the package's own final name, as pi splits the ref off at the path's
// first "@"; and a list entry loads the tree by the one rule an owner reads (loadsTree, PPX-D36), so
// a folder inside the tree holds it and a path that only begins with it does not.

import "testing"

// A REF WITH A SLASH: `@feat/x` is the ref, so the final name is the package's.
func TestTheDuplicateLoadLintNamesARemoteEntryWhoseRefHasASlash(t *testing.T) {
	tree := "~/" + treeInto
	for _, remote := range []string{
		"git:github.com/nicobailon/pi-subagents@feat/x",
		"https://github.com/nicobailon/pi-subagents@fix/y",
		"git:git@github.com:nicobailon/pi-subagents@release/1.x",
		"git:git@github.com:nicobailon/pi-subagents.git",
	} {
		if got := LintDuplicateLoads(agentPack(t, "matt", patchedTreeContribution(), piPackagesList(remote, tree))); len(got) != 1 {
			t.Errorf("%s beside %s: lint = %q, want one warning", remote, tree, got)
		}
	}
	if name, ok := remotePackageName("https://user@host.example/x/pi-foo"); !ok || name != "pi-foo" {
		t.Errorf("a URL with a user: final name %q (%v), want pi-foo", name, ok)
	}
}

// AN ENTRY INSIDE THE TREE holds it, and one that leaves it by `..` does not.
func TestTheDuplicateLoadLintReadsTheTreeAsAnOwnerDoes(t *testing.T) {
	remote := "git:github.com/nicobailon/pi-subagents"
	for _, c := range []struct {
		entry string
		want  int
	}{
		{"~/" + treeInto + "/packages/a", 1},
		{"~/" + treeInto + "/", 1},
		{"~/" + treeInto + "/../x", 0},
		{"~/" + treeInto + "-other", 0},
	} {
		if got := LintDuplicateLoads(agentPack(t, "matt", patchedTreeContribution(), piPackagesList(remote, c.entry))); len(got) != c.want {
			t.Errorf("%s beside %s: lint = %q, want %d warning(s)", c.entry, remote, got, c.want)
		}
	}
}

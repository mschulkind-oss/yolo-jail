package packsrc

// naming_test.go pins each entry's namespace and landing (docs/design/pack-naming.md §3.3, §3.4)
// on the design's worked example, §3.4.1, through ParseSourceDefinitions and ParsePackReference,
// the two calls config reads a TOML `packs` list with.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func obj(kv ...any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

// workedExampleSources is §3.4.1's [pack_sources].
func workedExampleSources(t *testing.T) map[string]SourceDefinition {
	t.Helper()
	defs := obj(
		"dotfiles", obj("path", "~/.dotfiles/yolo-packs", "bare", true),
		"work", "~/src/work-packs",
		"acme", obj("git", "https://github.com/acme/yolo-packs"),
	)
	sources, err := ParseSourceDefinitions(defs)
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

func TestTheWorkedExampleNamesEveryEntry(t *testing.T) {
	sources := workedExampleSources(t)
	shipped := map[string]bool{"claude": true}
	for _, tc := range []struct {
		entry     any
		namespace string
		landing   string
	}{
		{"claude", "", ""},
		{"dotfiles/matt", "", "matt"},
		{"dotfiles/matt-fzf", "", "matt-fzf"},
		{"work/deploy", "work", "work-deploy"},
		{"acme/tools", "acme", "acme-tools"},
		{"acme/lint", "acme", "acme-lint"},
		{obj("pack", "local/scratch", "bare", true), "", "scratch"},
		{obj("git", "https://github.com/alice/tools"), "tools", "tools"},
		{obj("git", "https://github.com/bob/tools", "namespace", "bob"), "bob", "bob"},
	} {
		ref, err := ParsePackReference(tc.entry, sources, "/home/u/.config/yolo-jail/packs", "/home/u",
			"config.packs[0]", shipped)
		if err != nil {
			t.Errorf("%v: %v", tc.entry, err)
			continue
		}
		if ref.Namespace != tc.namespace || ref.Landing != tc.landing {
			t.Errorf("%v: namespace %q landing %q, want %q and %q", tc.entry, ref.Namespace, ref.Landing,
				tc.namespace, tc.landing)
		}
	}
}

// Two entries that chose one word are told apart by NamespaceOwner, which is what
// packload.NameCollisions refuses on: alice's and bob's repositories both default to `tools`.
func TestTwoRepositoriesNamedAlikeShareAWordButNotAnOwner(t *testing.T) {
	alice, err := ParsePackReference(obj("git", "https://github.com/alice/tools"), nil, "", "/home/u", "p[0]", nil)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := ParsePackReference(obj("git", "https://github.com/bob/tools"), nil, "", "/home/u", "p[1]", nil)
	if err != nil {
		t.Fatal(err)
	}
	if alice.Namespace != bob.Namespace || alice.NamespaceOwner == bob.NamespaceOwner {
		t.Errorf("alice %+v and bob %+v: want one word, two owners", alice, bob)
	}
	sources := workedExampleSources(t)
	tools, _ := ParsePackReference("acme/tools", sources, "", "/home/u", "p[2]", nil)
	lint, _ := ParsePackReference("acme/lint", sources, "", "/home/u", "p[3]", nil)
	if tools.NamespaceOwner != lint.NamespaceOwner {
		t.Errorf("two packs of one source have different owners: %q, %q", tools.NamespaceOwner, lint.NamespaceOwner)
	}
}

// A user-written word must already be legal, and the refusal prints the legal spelling (§3.3).
func TestAUserWrittenWordIsRefusedWithItsLegalSpelling(t *testing.T) {
	if _, err := ParseSourceDefinitions(obj("Work_Packs", "~/w")); err == nil ||
		!strings.Contains(err.Error(), `"work-packs"`) {
		t.Errorf("an illegal source key was not refused with its legal spelling: %v", err)
	}
	_, err := ParsePackReference(obj("git", "https://github.com/bob/tools", "namespace", "Bob"), nil, "",
		"/home/u", "p[0]", nil)
	if err == nil || !strings.Contains(err.Error(), `"bob"`) {
		t.Errorf("an illegal namespace was not refused with its legal spelling: %v", err)
	}
	// A source's namespace is its key, with no second spelling (PN-D11).
	_, err = ParsePackReference(obj("pack", "acme/tools", "namespace", "x"), workedExampleSources(t), "",
		"/home/u", "p[0]", nil)
	if err == nil || !strings.Contains(err.Error(), "yolo pack source rename acme NEW") {
		t.Errorf("a namespace on a source's pack was not refused naming the rename verb: %v", err)
	}
	// A landing past the limit every agent accepts is refused.
	long := strings.Repeat("a", 70)
	if _, err := ParsePackReference(obj("git", "https://github.com/bob/"+long), nil, "", "/home/u", "p[0]",
		nil); err == nil || !strings.Contains(err.Error(), "64") {
		t.Errorf("a 70-character namespace was not refused: %v", err)
	}
}

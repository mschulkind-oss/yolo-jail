package packsrc

// naming_test.go pins each entry's namespace and landing (docs/reference/pack-system.md#pack-namespace, #delivered-names)
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
		{"dotfiles/matt", "", ""}, // bare: no landing of its own, so main's spelling holds (PN-D7)
		{"dotfiles/matt-fzf", "", ""},
		{"work/deploy", "work", "work-deploy"},
		{"acme/tools", "acme", "acme-tools"},
		{"acme/lint", "acme", "acme-lint"},
		{obj("pack", "local/scratch", "bare", true), "", ""},
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
	// A source key that is not a word LOADS (main's migrator wrote folder names as keys), and is
	// refused only where a namespace is composed from it, naming its legal spelling and the exact
	// line that keeps its packs bare.
	sources, err := ParseSourceDefinitions(obj("Work_Packs", "~/w", "Git_Packs", obj("git", "https://github.com/a/b")))
	if err != nil {
		t.Fatalf("a source key that is no namespace word must still load: %v", err)
	}
	for _, tc := range []struct{ handle, step string }{
		{"Work_Packs/x", "`yolo config set pack_sources.Work_Packs '{ path = \"~/w\", bare = true }' --user`"},
		{"Git_Packs/x", "`yolo config set pack_sources.Git_Packs.bare true --user`"},
	} {
		_, err = ParsePackReference(tc.handle, sources, "", "/home/u", "p[0]", nil)
		if err == nil || !strings.Contains(err.Error(), `"`+strings.ToLower(strings.ReplaceAll(strings.Split(tc.handle, "/")[0], "_", "-"))+`"`) ||
			!strings.Contains(err.Error(), tc.step) {
			t.Errorf("%s: not refused with its legal spelling and %s: %v", tc.handle, tc.step, err)
		}
	}
	_, err = ParsePackReference(obj("git", "https://github.com/bob/tools", "namespace", "Bob"), nil, "",
		"/home/u", "p[0]", nil)
	if err == nil || !strings.Contains(err.Error(), `"bob"`) {
		t.Errorf("an illegal namespace was not refused with its legal spelling: %v", err)
	}
	// A source's namespace is its key, with no second spelling (PN-D11).
	_, err = ParsePackReference(obj("pack", "acme/tools", "namespace", "x"), workedExampleSources(t), "",
		"/home/u", "p[0]", nil)
	if err == nil || !strings.Contains(err.Error(), "rename the key under [pack_sources]") {
		t.Errorf("a namespace on a source's pack was not refused naming the hand rename: %v", err)
	}
	// A landing past the limit every agent accepts is refused.
	long := strings.Repeat("a", 70)
	if _, err := ParsePackReference(obj("git", "https://github.com/bob/"+long), nil, "", "/home/u", "p[0]",
		nil); err == nil || !strings.Contains(err.Error(), "64") {
		t.Errorf("a 70-character namespace was not refused: %v", err)
	}
}

// M3 (PN-D7: bare means unchanged): a bare pack keeps main's landing and /ctx spelling, so it has
// no Landing of its own, and no character or length rule applies to its names.
func TestABarePackIsNamedExactlyAsBefore(t *testing.T) {
	sources, err := ParseSourceDefinitions(obj("Matt_Tools", obj("path", "~/src/Matt_Tools", "bare", true)))
	if err != nil {
		t.Fatalf("a bare source with a folder-name key must load: %v", err)
	}
	long := strings.Repeat("A_", 40)
	for _, entry := range []any{"Matt_Tools/Matt_Tools", "Matt_Tools/" + long,
		obj("pack", "local/Matt_Tools", "bare", true), obj("git", "https://github.com/bob/"+long, "bare", true)} {
		ref, err := ParsePackReference(entry, sources, "/home/u/.config/yolo-jail/packs", "/home/u", "p[0]", nil)
		if err != nil {
			t.Errorf("%v: a bare pack was refused: %v", entry, err)
			continue
		}
		if ref.Namespace != "" || ref.Landing != "" || ref.KeepNamesStep != "" {
			t.Errorf("%v: namespace %q landing %q step %q, want all empty (named as before)", entry,
				ref.Namespace, ref.Landing, ref.KeepNamesStep)
		}
	}
}

// A source key that is not a plain word is quoted as one component of the config-set path, and the
// whole path is shell-quoted; a handle `yolo pack add` checks is not in the config yet, so its step
// adds the table instead (second-round review, majors 1 and 2).
func TestTheBareStepQuotesTheKeyAndNamesARealCommand(t *testing.T) {
	for _, tc := range []struct{ key, want string }{
		{"work", "`yolo config set pack_sources.work.bare true --user`"},
		{"skills.git", "`yolo config set 'pack_sources.\"skills.git\".bare' true --user`"},
		{"my tools", "`yolo config set 'pack_sources.\"my tools\".bare' true --user`"},
	} {
		if got := (SourceDefinition{Name: tc.key, Marked: true}).KeepNamesStep(); got != tc.want {
			t.Errorf("%q: %s, want %s", tc.key, got, tc.want)
		}
	}
	ref, err := ParsePackReference("local/mine", nil, "/home/u/.config/yolo-jail/packs", "/home/u", "pack handle", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "`yolo config add packs '{ pack = \"local/mine\", bare = true }' --user`"; ref.KeepNamesStep != want {
		t.Errorf("pack add's step = %s, want %s", ref.KeepNamesStep, want)
	}
	if want := "`yolo config add packs '{ pack = \"local/mine\", bare = false }' --user`"; ref.AcceptNamesStep != want {
		t.Errorf("pack add's accept step = %s, want %s", ref.AcceptNamesStep, want)
	}
}

// M5: an entry and source that chose nothing get the default namespace, and carry the one step
// that keeps their names bare; one that chose (either way) carries none.
func TestADefaultedNamespaceCarriesTheStepThatKeepsItsNames(t *testing.T) {
	sources, err := ParseSourceDefinitions(obj("work", "~/w", "acme", obj("git", "https://github.com/a/b", "bare", false)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		entry any
		step  string
	}{
		{"work/deploy", "`yolo config set pack_sources.work '{ path = \"~/w\", bare = true }' --user`"},
		{"acme/tools", ""},
		{obj("pack", "work/deploy", "bare", false), ""},
		{"local/mine", "`yolo config set 'packs[3]' '{ pack = \"local/mine\", bare = true }' --user`"},
		{obj("git", "https://github.com/bob/tools"), "`yolo config set 'packs[3].bare' true --user`"},
		{obj("pack", "local/mine", "namespace", "me"), ""},
	} {
		ref, err := ParsePackReference(tc.entry, sources, "/home/u/.config/yolo-jail/packs", "/home/u", "config.packs[3]", nil)
		if err != nil {
			t.Fatalf("%v: %v", tc.entry, err)
		}
		if (tc.step == "") != (ref.KeepNamesStep == "") || !strings.Contains(ref.KeepNamesStep, tc.step) {
			t.Errorf("%v: step %q, want %q", tc.entry, ref.KeepNamesStep, tc.step)
		}
	}
}

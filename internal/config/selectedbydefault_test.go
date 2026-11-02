package config

// selectedbydefault_test.go pins PD-13 of docs/reference/loophole-grants.md: a shipped
// pack whose manifest says `selected_by_default = true` is in every selection with no `packs`
// line naming it, appended beside the conventional local pack, and a configured pack may not
// declare the key.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// withoutDefaultSelected drops the entries a shipped pack's `selected_by_default` appended, for
// a test about the configured entries and the local pack.
func withoutDefaultSelected(entries []PackEntry) []PackEntry {
	return WithoutSelectedByDefault(entries)
}

// THE SHIPPED SET is cgroup-delegate alone (LG-D13): adding a pack here puts it in every launch
// on every machine, which is a ruling, not an edit.
func TestOnlyTheCgroupDelegateIsSelectedByDefault(t *testing.T) {
	if got := packload.EmbeddedSelectedByDefault(); !reflect.DeepEqual(got, []string{"cgroup-delegate"}) {
		t.Errorf("default-selected shipped packs = %v, want [cgroup-delegate] (LG-D13)", got)
	}
}

// A config with no `packs` key selects cgroup-delegate, marked ByDefault and embedded, and it is
// not a configured pack, so the "no coding agent" notice still applies.
func TestLoadPacksSelectsTheDefaultPackWithNoPacksKey(t *testing.T) {
	localPackHome(t, `{}`, false)
	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "cgroup-delegate" || !entries[0].ByDefault ||
		!entries[0].Embedded() || entries[0].Implicit {
		t.Fatalf("entries = %+v, want the one default-selected cgroup-delegate entry", entries)
	}
	if HasConfiguredPack(entries) {
		t.Error("a default-selected pack counts as configured, so a jail with no agent stops saying so")
	}
	// And the selection every reader resolves (resolveSelectedPacks) holds the pack.
	packs, complete := resolveSelectedPacks()
	if !complete || len(packs) != 1 || packs[0].Name != "cgroup-delegate" || !packs[0].SelectedByDefault() {
		t.Fatalf("resolved selection = %v (complete %v), want cgroup-delegate", packNames(packs), complete)
	}
}

// ORDER: after the configured entries and before the local pack, which stays last.
func TestDefaultPackComposesBeforeTheLocalPack(t *testing.T) {
	localPackHome(t, `{"packs": ["claude"]}`, true)
	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if want := []string{"claude", "cgroup-delegate", LocalPackName}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entry order = %v, want %v", names, want)
	}
}

// A `packs` line already naming the pack, under either handle, wins the slot: one entry, not two
// sharing a staging dir, and it is the configured one.
func TestAConfiguredLineForTheDefaultPackIsNotDoubled(t *testing.T) {
	localPackHome(t, `{"packs": ["cgroup-delegate"]}`, false)
	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ByDefault {
		t.Fatalf("cgroup-delegate: entries = %+v, want the one configured entry", entries)
	}
	home := readablePackTestHome(t)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = ['yolo/cgroup-delegate']\n")
	entries, err = LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ByDefault || entries[0].EmbeddedName != "cgroup-delegate" {
		t.Fatalf("yolo/cgroup-delegate: entries = %+v, want the one configured entry", entries)
	}
}

// A CONFIGURED pack declaring the key is refused when it resolves, naming the line to delete:
// only a pack yolo ships may put itself in every launch.
func TestAConfiguredPackDeclaringSelectedByDefaultIsRefused(t *testing.T) {
	localPackHome(t, `{}`, false)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.toml"),
		[]byte("name = \"mine\"\nselected_by_default = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ResolvePack(PackEntry{Source: "file://" + dir, Name: "mine"}, ResolvePackSpec{ReadOnlyStore: true})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Problems, "\n")
	for _, want := range []string{"pack mine", "selected_by_default", "shipped with yolo", "delete that line"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems %q do not say %q", res.Problems, want)
		}
	}
	// The shipped pack itself, resolved the same way, has no such problem.
	shipped, err := ResolvePack(EmbeddedPackEntry("cgroup-delegate"), ResolvePackSpec{ReadOnlyStore: true,
		Dest: filepath.Join(t.TempDir(), "cgroup-delegate")})
	if err != nil || len(shipped.Problems) != 0 {
		t.Fatalf("the shipped pack staged with problems %v (err %v)", shipped.Problems, err)
	}
	if !shipped.Pack.SelectedByDefault() {
		t.Error("a staged copy of the shipped pack lost SelectedByDefault")
	}
}

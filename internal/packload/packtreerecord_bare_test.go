package packload

import (
	"path/filepath"
	"testing"
)

// The pack tree record carries each entry's exact bare command, so an attach, which reads the
// running jail's tree instead of the config, refuses two names a namespace delivers alike with
// the same command the launch prints (docs/reference/pack-system.md#pack-name-clashes).
func TestThePackTreeRecordCarriesTheBareCommand(t *testing.T) {
	root := t.TempDir()
	const step = "`yolo config set 'pack_sources.acme.bare' true --user`"
	launched := &Pack{Name: "acme/tools", Root: filepath.Join(root, "acme_2ftools"),
		Namespace: "acme", NamespaceOwner: "source:acme", BareStep: step}
	if err := WritePackTreeRecord(root, []*Pack{launched}); err != nil {
		t.Fatal(err)
	}
	entries, recorded, err := ReadPackTreeRecord(root)
	if err != nil || !recorded || len(entries) != 1 {
		t.Fatalf("record = %v, recorded=%v, err=%v", entries, recorded, err)
	}
	attached := &Pack{Name: "acme/tools"}
	entries[0].ApplyNaming(attached)
	if attached.BareStep != step || attached.Namespace != "acme" {
		t.Errorf("an attach reads BareStep %q and namespace %q, want %q and acme",
			attached.BareStep, attached.Namespace, step)
	}
}

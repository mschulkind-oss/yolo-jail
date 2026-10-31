package entrypoint

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/slug"
)

// TestTheJailNamesARecordedPackAsTheLaunchDid: a readable-config pack is named by its source
// handle, which holds a "/" (OQ-RC15), and stages under that name's slug. The jail once named it by
// the staged directory, so every key the host built on the name and the jail rebuilt on its own
// differed: a fork's key (`<pack>/<bin>`, which the host's patched-tree wire is keyed on) read
// `src_2ffork/tool` in the jail against `src/fork/tool` at the host, and a `fork_of` naming a
// configured base by its handle found no pack of that name at all.
func TestTheJailNamesARecordedPackAsTheLaunchDid(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "pack-tree")
	stage := func(name, manifest string) *packload.Pack {
		t.Helper()
		dir := filepath.Join(tree, slug.Escape(name))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePackManifest(t, filepath.Join(dir, "pack.json"), manifest)
		p, problems := packload.LoadDir(dir, name)
		if len(problems) > 0 {
			t.Fatal(problems)
		}
		return p
	}
	base := stage("basepack", `{"contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`)
	fork := stage("src/fork", `{"contributes":[{"kind":"program","bin":"tool","via":"source",`+
		`"fork_of":"basepack","source":"git+https://example.invalid/upstream?ref=main",`+
		`"build":"sh build.sh","produces":[".local/bin/tool"]}]}`)
	host := []*packload.Pack{base, fork}
	if err := packload.WritePackTreeRecord(tree, host); err != nil {
		t.Fatal(err)
	}
	hostKeys := forkKeys(packload.Forks(host))
	if !slices.Equal(hostKeys, []string{"src/fork/tool"}) {
		t.Fatalf("fixture: host fork keys = %v, want [src/fork/tool]", hostKeys)
	}

	jail, err := loadPackRootAsStaged(NewEnv(map[string]string{"YOLO_PACK_ROOT": tree}), tree)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range jail {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"basepack", "src/fork"}) {
		t.Errorf("the jail names the packs %v, want the launch's names [basepack src/fork]", names)
	}
	if got := forkKeys(packload.Forks(jail)); !slices.Equal(got, hostKeys) {
		t.Errorf("jail fork keys = %v, want the host's %v — the patched-tree wire and every "+
			"fork record are keyed on them", got, hostKeys)
	}
	for _, p := range jail {
		if p.StagedSlug() != slug.Escape(p.Name) {
			t.Errorf("pack %s: StagedSlug = %q, want its directory %q", p.Name, p.StagedSlug(), slug.Escape(p.Name))
		}
	}
}

func forkKeys(forks []packload.Fork) []string {
	var out []string
	for _, f := range forks {
		out = append(out, f.Key())
	}
	return out
}

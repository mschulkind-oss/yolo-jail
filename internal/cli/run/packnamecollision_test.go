package run

// packnamecollision_test.go drives the launch's pack-name pre-flight (docs/design/pack-naming.md
// §3.5) through the real stagePacks, so it fails if the call to packload.NameCollisions is
// deleted: `local/acme` is its own namespace, `acme`, which is also the source `acme`'s.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// namespaceClashConfig writes a TOML user config selecting `acme/x` from the source `acme` and
// the local pack `local/acme`, and returns the home it is in.
func namespaceClashConfig(t *testing.T, packs string) string {
	t.Helper()
	home := packHome(t)
	src := t.TempDir()
	for _, dir := range []string{filepath.Join(src, "x"), filepath.Join(home, ".config", "yolo-jail", "packs", "acme")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePack(t, dir, `{"contributes":[]}`)
	}
	body := "packs = " + packs + "\n\n[pack_sources]\nacme = '" + filepath.ToSlash(src) + "'\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestALaunchRefusesTwoEntriesInOneNamespace(t *testing.T) {
	namespaceClashConfig(t, `["acme/x", "local/acme"]`)
	o := &Options{Workspace: t.TempDir(), Stdout: &strings.Builder{}}
	_, _, _, err := o.stagePacks("yolo-test-pack-namespace")
	if err == nil {
		t.Fatal("stagePacks accepted local/acme beside the source acme: both are the namespace acme")
	}
	for _, want := range []string{"acme/x", "local/acme", `namespace "acme"`, "renaming the [pack_sources] key acme"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q:\n%v", want, err)
		}
	}
}

// Marking the local pack bare takes it out of the namespace, and the launch proceeds.
func TestALaunchAcceptsABareEntryBesideASourceOfItsName(t *testing.T) {
	namespaceClashConfig(t, `["acme/x", { pack = "local/acme", bare = true }]`)
	o := &Options{Workspace: t.TempDir(), Stdout: &strings.Builder{}}
	if _, _, _, err := o.stagePacks("yolo-test-pack-namespace-bare"); err != nil &&
		strings.Contains(err.Error(), "namespace") {
		t.Fatalf("a bare entry was refused as a namespace clash: %v", err)
	}
}

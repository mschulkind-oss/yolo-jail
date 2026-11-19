package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestValidateExplicitEditNeverFetchedScopedSelectionIsUnverified(t *testing.T) {
	home := readablePackTestHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(home, ".config/yolo-jail")
	physical := filepath.Join(root, "config.toml")
	body := `packs = [{git="https://example.invalid/fixture/corpus", ref="main", skills={only=["chosen"]}}]` + "\n"
	write(t, physical, body)
	input, err := NewConfigReadInput(physical, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	target, err := NewExplicitEditTarget(workspace, physical, ExplicitEditUser, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	before := explicitEditTreeSnapshot(t, root, paths.PacksDir())
	result, err := ValidateExplicitEdit(workspace, target, input)
	if err != nil || len(result.UnverifiedPacks()) != 1 || !strings.Contains(result.UnverifiedPacks()[0], "unverified") {
		t.Fatalf("missing scoped content failed instead of unverified: %+v %v", result, err)
	}
	if after := explicitEditTreeSnapshot(t, root, paths.PacksDir()); after != before {
		t.Fatal("unverified validation wrote source/store")
	}
}

func TestValidateExplicitEditProjectsScopedRawInsteadOfPoisonManifest(t *testing.T) {
	home := readablePackTestHome(t)
	t.Setenv("YOLO_VERSION", "")
	root := filepath.Join(home, ".config/yolo-jail")
	write(t, filepath.Join(root, "packs/corpus/pack.toml"), "broken TOML [\n")
	write(t, filepath.Join(root, "packs/corpus/raw/chosen/SKILL.md"), "chosen\n")
	physical := filepath.Join(root, "config.toml")
	for _, tc := range []struct{ name, selector, want string }{{"valid", "chosen", ""}, {"stale", "missing", "available names"}} {
		t.Run(tc.name, func(t *testing.T) {
			body := `packs = [{pack="local/corpus", skills={dir="raw", only=["` + tc.selector + `"]}}]` + "\n"
			write(t, physical, body)
			input, err := NewConfigReadInput(physical, []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			workspace := t.TempDir()
			target, err := NewExplicitEditTarget(workspace, physical, ExplicitEditUser, configdoc.TOML)
			if err != nil {
				t.Fatal(err)
			}
			before := explicitEditTreeSnapshot(t, root, paths.PacksDir())
			_, err = ValidateExplicitEdit(workspace, target, input)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("scoped edit: %v", err)
			}
			if after := explicitEditTreeSnapshot(t, root, paths.PacksDir()); after != before {
				t.Fatal("pure edit validation wrote source/store")
			}
		})
	}
	if _, err := os.Lstat(paths.PacksDir()); !os.IsNotExist(err) {
		t.Fatalf("edit created store: %v", err)
	}
}

package packs_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// These frozen legacy manifests and their grouped-TOML equivalents describe exactly the
// six simple bundled loophole contributions proposed for a later source conversion. The
// source-semantics check makes the fixtures fail closed if a shipped manifest drifts first.
func TestLane3BundledLoopholeTOMLSemanticEquivalence(t *testing.T) {
	cases := []struct {
		name   string
		legacy string
		toml   string
	}{
		{
			name:   "host-processes",
			legacy: `{"contributes":[{"from":"loopholes/host-processes","kind":"loophole"}],"name":"host-processes"}`,
			toml:   "name = \"host-processes\"\n\n[[loophole]]\nfrom = \"loopholes/host-processes\"\n",
		},
		{
			name:   "journal",
			legacy: `{"contributes":[{"from":"loopholes/journal","kind":"loophole"}],"name":"journal"}`,
			toml:   "name = \"journal\"\n\n[[loophole]]\nfrom = \"loopholes/journal\"\n",
		},
		{
			name:   "macos-log",
			legacy: `{"contributes":[{"from":"loopholes/macos-log","kind":"loophole"}],"name":"macos-log"}`,
			toml:   "name = \"macos-log\"\n\n[[loophole]]\nfrom = \"loopholes/macos-log\"\n",
		},
		{
			name:   "cgroup-delegate",
			legacy: `{"contributes":[{"from":"loopholes/cgroup-delegate","kind":"loophole"}],"name":"cgroup-delegate","selected_by_default":true}`,
			toml:   "name = \"cgroup-delegate\"\nselected_by_default = true\n\n[[loophole]]\nfrom = \"loopholes/cgroup-delegate\"\n",
		},
		{
			name:   "serial",
			legacy: `{"name":"serial","contributes":[{"from":"loopholes/serial","kind":"loophole"}]}`,
			toml:   "name = \"serial\"\n\n[[loophole]]\nfrom = \"loopholes/serial\"\n",
		},
		{
			name:   "hello-daemon",
			legacy: `{"name":"hello-daemon","contributes":[{"from":"loopholes/hello-daemon","kind":"loophole"}]}`,
			toml:   "name = \"hello-daemon\"\n\n[[loophole]]\nfrom = \"loopholes/hello-daemon\"\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shippedRoot := filepath.Join(".", tc.name)
			shippedManifest, err := os.ReadFile(filepath.Join(shippedRoot, packdecl.ManifestTOMLName))
			if err != nil {
				t.Fatal(err)
			}
			if string(shippedManifest) != tc.toml {
				t.Fatalf("shipped %s/%s differs from the frozen TOML fixture:\n%s", tc.name, packdecl.ManifestTOMLName, shippedManifest)
			}
			shippedDecl, shippedProblems := packdecl.DecodeTOML(shippedManifest)
			frozenDecl, frozenProblems := packdecl.Decode([]byte(tc.legacy))
			if len(shippedProblems) != 0 || len(frozenProblems) != 0 {
				t.Fatalf("decode shipped/frozen legacy manifest: shipped=%v frozen=%v", shippedProblems, frozenProblems)
			}
			if !reflect.DeepEqual(lane3ManifestSemantics(shippedDecl), lane3ManifestSemantics(frozenDecl)) {
				t.Fatalf("frozen legacy fixture no longer means what shipped %s/%s does", tc.name, packdecl.ManifestTOMLName)
			}

			legacyRoot := t.TempDir()
			tomlRoot := t.TempDir()
			writeLane3Fixture(t, filepath.Join(legacyRoot, packdecl.ManifestLegacyJSONName), []byte(tc.legacy), 0o644)
			writeLane3Fixture(t, filepath.Join(tomlRoot, packdecl.ManifestTOMLName), []byte(tc.toml), 0o644)

			moduleRel := filepath.Join("loopholes", tc.name)
			shippedModule := filepath.Join(shippedRoot, moduleRel)
			if err := copyLane3Tree(shippedModule, filepath.Join(legacyRoot, moduleRel)); err != nil {
				t.Fatalf("copy legacy loophole assets: %v", err)
			}
			if err := copyLane3Tree(shippedModule, filepath.Join(tomlRoot, moduleRel)); err != nil {
				t.Fatalf("copy TOML loophole assets: %v", err)
			}
			assertLane3TreeEqual(t, shippedModule, filepath.Join(legacyRoot, moduleRel))
			assertLane3TreeEqual(t, shippedModule, filepath.Join(tomlRoot, moduleRel))

			// LoadDir is the production manifest discovery/decoder caller. LoopholeModules
			// is the production contribution resolver and nested JSONC decoder.
			legacyPack := loadLane3Pack(t, legacyRoot, tc.name)
			tomlPack := loadLane3Pack(t, tomlRoot, tc.name)
			if legacyPack.Name != tc.name || tomlPack.Name != tc.name {
				t.Fatalf("pack identities = %q and %q, want %q", legacyPack.Name, tomlPack.Name, tc.name)
			}
			legacyManifest := lane3ManifestSemantics(legacyPack.Decl)
			tomlManifest := lane3ManifestSemantics(tomlPack.Decl)
			if !reflect.DeepEqual(legacyManifest, tomlManifest) {
				t.Fatalf("decoded manifest semantics differ:\nlegacy: %#v\nTOML:   %#v", legacyManifest, tomlManifest)
			}
			if legacyPack.Decl.Name != tc.name || len(legacyPack.Decl.Contributes) != 1 ||
				legacyPack.Decl.Contributes[0].Kind != packdecl.KindLoophole ||
				legacyPack.Decl.Contributes[0].From != moduleRel {
				t.Fatalf("legacy fixture no longer resolves the expected identity/permission contribution: %#v", legacyPack.Decl)
			}
			if tomlPack.Decl.Origin.Format != packdecl.ManifestFormatTOML {
				t.Fatalf("production loader did not select TOML reader: origin=%#v", tomlPack.Decl.Origin)
			}

			legacyModules := lane3LoopholeModules(t, legacyPack)
			tomlModules := lane3LoopholeModules(t, tomlPack)
			if !reflect.DeepEqual(legacyModules, tomlModules) {
				t.Fatalf("resolved loophole semantics differ:\nlegacy: %#v\nTOML:   %#v", legacyModules, tomlModules)
			}
			if len(legacyModules) != 1 || legacyModules[0].name != tc.name || legacyModules[0].from != moduleRel {
				t.Fatalf("resolved loophole identity differs from frozen fixture: %#v", legacyModules)
			}
		})
	}
}

type lane3LoopholeSemantics struct {
	from string
	name string
	decl *loopholedecl.Manifest
}

func loadLane3Pack(t *testing.T, root, name string) *packload.Pack {
	t.Helper()
	p, problems := packload.LoadDir(root, name)
	if len(problems) != 0 {
		t.Fatalf("production LoadDir(%q): %v", name, problems)
	}
	if p == nil || p.Decl == nil {
		t.Fatalf("production LoadDir(%q) returned no pack/declaration", name)
	}
	return p
}

func lane3ManifestSemantics(decl *packdecl.Manifest) packdecl.Manifest {
	out := *decl
	out.Origin = packdecl.ManifestOrigin{}
	out.Contributes = append([]packdecl.Contribution(nil), decl.Contributes...)
	for i := range out.Contributes {
		// Source format and source locations intentionally differ between JSON and TOML;
		// all semantic contribution fields, including Replace, remain in the comparison.
		out.Contributes[i].Origin = packdecl.ManifestOrigin{}
		out.Contributes[i].Location = packdecl.ManifestLocation{}
	}
	return out
}

func lane3LoopholeModules(t *testing.T, p *packload.Pack) []lane3LoopholeSemantics {
	t.Helper()
	modules, refusals, warnings := p.LoopholeModules()
	if len(refusals) != 0 || len(warnings) != 0 {
		t.Fatalf("production loophole resolution: refusals=%v warnings=%v", refusals, warnings)
	}
	out := make([]lane3LoopholeSemantics, 0, len(modules))
	for _, module := range modules {
		if module.Decl == nil || module.Problem != "" {
			t.Fatalf("nested loophole manifest did not decode: %#v", module)
		}
		out = append(out, lane3LoopholeSemantics{
			from: module.From,
			name: module.Name,
			decl: module.Decl,
		})
	}
	return out
}

func writeLane3Fixture(t *testing.T, path string, body []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func copyLane3Tree(src, dst string) error {
	var dirs []struct {
		path string
		mode fs.FileMode
	}
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return filepath.SkipAll
		}
		if info.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			dirs = append(dirs, struct {
				path string
				mode fs.FileMode
			}{target, info.Mode().Perm()})
			return nil
		}
		if !info.Mode().IsRegular() {
			return &fs.PathError{Op: "copy", Path: path, Err: os.ErrInvalid}
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, body, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chmod(target, info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].mode); err != nil {
			return err
		}
	}
	return nil
}

func assertLane3TreeEqual(t *testing.T, want, got string) {
	t.Helper()
	var compare func(string, string) error
	compare = func(wantDir, gotDir string) error {
		wantEntries, err := os.ReadDir(wantDir)
		if err != nil {
			return err
		}
		gotEntries, err := os.ReadDir(gotDir)
		if err != nil {
			return err
		}
		if len(wantEntries) != len(gotEntries) {
			return &fs.PathError{Op: "compare entries", Path: gotDir, Err: os.ErrNotExist}
		}
		for i, wantEntry := range wantEntries {
			gotEntry := gotEntries[i]
			if wantEntry.Name() != gotEntry.Name() {
				return &fs.PathError{Op: "compare name", Path: gotDir, Err: os.ErrInvalid}
			}
			wantPath, gotPath := filepath.Join(wantDir, wantEntry.Name()), filepath.Join(gotDir, gotEntry.Name())
			wantInfo, err := os.Lstat(wantPath)
			if err != nil {
				return err
			}
			gotInfo, err := os.Lstat(gotPath)
			if err != nil {
				return err
			}
			if wantInfo.Mode().Type() != gotInfo.Mode().Type() || wantInfo.Mode().Perm() != gotInfo.Mode().Perm() {
				return &fs.PathError{Op: "compare mode", Path: gotPath, Err: os.ErrPermission}
			}
			if wantInfo.IsDir() {
				if err := compare(wantPath, gotPath); err != nil {
					return err
				}
				continue
			}
			wantBody, err := os.ReadFile(wantPath)
			if err != nil {
				return err
			}
			gotBody, err := os.ReadFile(gotPath)
			if err != nil {
				return err
			}
			if !bytes.Equal(wantBody, gotBody) {
				return &fs.PathError{Op: "compare content", Path: gotPath, Err: os.ErrInvalid}
			}
		}
		return nil
	}
	if err := compare(want, got); err != nil {
		t.Fatalf("loophole asset copy changed bytes, paths, or permissions: %v", err)
	}
}

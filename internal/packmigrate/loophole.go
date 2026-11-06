package packmigrate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// LoopholeMigration is one loophole module's planned rewrite from its legacy manifest.jsonc to
// loophole.toml (docs/design/readable-config.md RC-D28), proven equal before anything is written:
// the TOML decodes, strictly, to the identical in-memory manifest the legacy file did.
type LoopholeMigration struct {
	// Dir is the module directory, relative to the pack root.
	Dir string
	// Legacy is the file being replaced (manifest.jsonc or manifest.json).
	Legacy string
	// TOML is the new loophole.toml's bytes.
	TOML []byte
	// Comments is every legacy comment as carried into the new file, in document order.
	Comments []string

	legacyBytes []byte
	want        *loopholedecl.Manifest
}

// PlanLoophole plans one module directory's migration. It returns (nil, nil) for a module with
// nothing to migrate (it already holds loophole.toml and no legacy file), and refuses a module
// holding both, whose manifest does not read strictly, or holding a value TOML cannot say.
func PlanLoophole(root, rel string) (*LoopholeMigration, error) {
	moduleDir := filepath.Join(root, filepath.FromSlash(rel))
	var legacy string
	for _, name := range []string{loopholedecl.ManifestName, "manifest.json"} {
		info, err := os.Lstat(filepath.Join(moduleDir, name))
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: %s is not a regular file; replace it with the file it names, then migrate", rel, name)
		}
		if legacy != "" {
			return nil, fmt.Errorf("%s holds both %s and %s; delete one, then migrate", rel, legacy, name)
		}
		legacy = name
	}
	if legacy == "" {
		return nil, nil
	}
	if _, err := os.Lstat(filepath.Join(moduleDir, loopholedecl.ManifestTOMLName)); err == nil {
		return nil, fmt.Errorf("%s holds both %s and %s; delete %s (the TOML file is the one a yolo reads), then run migrate again if the TOML is not the one you want",
			rel, loopholedecl.ManifestTOMLName, legacy, legacy)
	}
	data, err := os.ReadFile(filepath.Join(moduleDir, legacy))
	if err != nil {
		return nil, err
	}
	want, err := loopholedecl.Decode(data, moduleDir)
	if err != nil {
		return nil, fmt.Errorf("%s: %s does not read cleanly, so it cannot be proven equal after migration:\n  %s\nFix those, check with `yolo pack lint`, then migrate",
			rel, legacy, strings.Join(problemsOf(err), "\n  "))
	}
	// The legacy window (LC-D10): what loophole.toml cannot say is the author's to restate,
	// so it is refused naming the restatement; what reads as nothing is dropped.
	var restate []string
	for _, note := range loopholedecl.LegacyNotes(moduleDir) {
		if note.Restate {
			restate = append(restate, note.Text)
		}
	}
	if len(restate) > 0 {
		return nil, fmt.Errorf("%s: %s says what %s cannot, so it is not converted:\n  %s",
			rel, legacy, loopholedecl.ManifestTOMLName, strings.Join(restate, "\n  "))
	}
	doc, err := configdoc.Parse(data, configdoc.JSONC)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", rel, legacy, err)
	}
	if v, ok := doc.Values.Get(loopholedecl.RetiredKeyDefaultEnabled); ok && v == true {
		doc.Values.Delete(loopholedecl.RetiredKeyDefaultEnabled)
	}
	lines, err := packdecl.EncodeLoopholeTOMLLines(doc.Values)
	if err != nil {
		var unrepresentable *packdecl.UnrepresentableError
		if errors.As(err, &unrepresentable) {
			return nil, fmt.Errorf("%s: %s: %w", rel, legacy, unloopholeable(unrepresentable))
		}
		return nil, fmt.Errorf("%s: %s: %w", rel, legacy, err)
	}
	lines, comments := placeComments(lines, doc)
	encoded := packdecl.JoinTOMLLines(lines)
	if err := loopholeTOMLProof(moduleDir, encoded, want); err != nil {
		return nil, fmt.Errorf("%s: %s: %w", rel, legacy, err)
	}
	return &LoopholeMigration{Dir: rel, Legacy: legacy, TOML: encoded, Comments: comments, legacyBytes: data, want: want}, nil
}

func problemsOf(err error) []string {
	var le *loopholedecl.Error
	if errors.As(err, &le) {
		return le.Problems()
	}
	return []string{err.Error()}
}

// unloopholeable rewords the pack encoder's refusal for a loophole manifest: the same refusal,
// without the advice that names pack.toml.
func unloopholeable(e *packdecl.UnrepresentableError) error {
	return fmt.Errorf("%s %s, which loophole.toml cannot say (TOML has no null); delete the key, or give it the value you meant, then migrate", e.Path, e.Reason)
}

// loopholeTOMLProof decodes the planned TOML the way a launch reads a loophole.toml and
// compares it with the legacy manifest, returning every difference.
func loopholeTOMLProof(moduleDir string, toml []byte, want *loopholedecl.Manifest) error {
	tmp, err := os.MkdirTemp("", "loophole-proof-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	// The module's directory basename is the loophole's name, so the proof module keeps it.
	module := filepath.Join(tmp, filepath.Base(moduleDir))
	if err := os.MkdirAll(module, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(module, loopholedecl.ManifestTOMLName), toml, 0o644); err != nil {
		return err
	}
	got, err := loopholedecl.LoadDir(module)
	if err != nil {
		return fmt.Errorf("the encoded loophole.toml does not read back: %v", err)
	}
	return LoopholeDifference(want, got)
}

// LoopholeDifference is nil when two decoded manifests are identical in every field.
func LoopholeDifference(want, got *loopholedecl.Manifest) error {
	if reflect.DeepEqual(want, got) {
		return nil
	}
	wv, gv := reflect.ValueOf(*want), reflect.ValueOf(*got)
	var diffs []string
	for i := 0; i < wv.NumField(); i++ {
		if !reflect.DeepEqual(wv.Field(i).Interface(), gv.Field(i).Interface()) {
			diffs = append(diffs, fmt.Sprintf("%s: legacy %v, TOML %v", wv.Type().Field(i).Name, wv.Field(i).Interface(), gv.Field(i).Interface()))
		}
	}
	return fmt.Errorf("the encoded loophole.toml would change what the loophole says:\n  %s", strings.Join(diffs, "\n  "))
}

// planLoopholes plans every module the pack's loophole contributions name.
func planLoopholes(dir string, sources []string) ([]LoopholeMigration, error) {
	var out []LoopholeMigration
	seen := map[string]bool{}
	for _, rel := range sources {
		rel = path.Clean(filepath.ToSlash(rel))
		if seen[rel] || !cleanRelative(rel) {
			continue
		}
		seen[rel] = true
		plan, err := PlanLoophole(dir, rel)
		if err != nil {
			return nil, err
		}
		if plan == nil {
			continue
		}
		out = append(out, *plan)
	}
	return out, nil
}

// unchanged reports whether the legacy file still holds the bytes the plan was made from.
func (l LoopholeMigration) unchanged(dir string) bool {
	current, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(l.Dir), l.Legacy))
	return err == nil && bytes.Equal(current, l.legacyBytes)
}

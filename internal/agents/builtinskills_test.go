package agents

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agents/builtinskills"
)

// repoBuiltinSkillsDir is the checkout's live builtinskills tree (tests run in
// the package directory).
func repoBuiltinSkillsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("builtinskills"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no checkout tree at %s: %v", dir, err)
	}
	return dir
}

// TestBuiltinSkillsEmbedMatchesTree is the sync guard for embed.go's EXPLICIT
// skill list: every skill dir on disk (a non-hidden subdir with a SKILL.md)
// must exist in the embed with byte-identical recursive contents, and vice
// versa. Adding a new built-in skill without extending the go:embed directive
// fails here. Mirrors internal/loopholes/TestEmbedMatchesTree.
func TestBuiltinSkillsEmbedMatchesTree(t *testing.T) {
	treeRoot := repoBuiltinSkillsDir(t)

	treeFiles := map[string][]byte{}
	entries, err := os.ReadDir(treeRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		if _, err := os.Stat(filepath.Join(treeRoot, e.Name(), "SKILL.md")); err != nil {
			continue // not a skill dir
		}
		err := filepath.WalkDir(filepath.Join(treeRoot, e.Name()), func(p string, d fs.DirEntry, werr error) error {
			if werr != nil || d.IsDir() {
				return werr
			}
			rel, rerr := filepath.Rel(treeRoot, p)
			if rerr != nil {
				return rerr
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			treeFiles[filepath.ToSlash(rel)] = b
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(treeFiles) == 0 {
		t.Fatal("found no skill files in the checkout tree — test is broken")
	}

	embedFiles := map[string][]byte{}
	err = fs.WalkDir(builtinskills.FS, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		b, rerr := fs.ReadFile(builtinskills.FS, p)
		if rerr != nil {
			return rerr
		}
		embedFiles[p] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var treeNames, embedNames []string
	for k := range treeFiles {
		treeNames = append(treeNames, k)
	}
	for k := range embedFiles {
		embedNames = append(embedNames, k)
	}
	sort.Strings(treeNames)
	sort.Strings(embedNames)
	if !reflect.DeepEqual(treeNames, embedNames) {
		t.Fatalf("embed/tree file sets differ (extend embed.go's go:embed list?)\ntree:  %v\nembed: %v", treeNames, embedNames)
	}
	for _, name := range treeNames {
		if !bytes.Equal(treeFiles[name], embedFiles[name]) {
			t.Errorf("embed/tree bytes differ for %s", name)
		}
	}
}

// maxDescBytes caps each shipped skill description. Descriptions are always-on
// context (only name+description load until a skill triggers), so this is a CI
// budget contract, replacing the old self-referential const-byte pin.
const maxDescBytes = 280

// maxFleetDescBytes bounds the SUM of built-in descriptions — a guard on OUR
// additions only (host user-level skills add their own always-on descriptions,
// which this cannot bound). Keep new built-ins tight or justify raising it.
const maxFleetDescBytes = 1400

// TestSkillFrontmatter enforces the shipped-skill contract with stdlib parsing
// only (no YAML dep — vendor/ is hermetic; a new module would break the image
// build while go test passes): valid name+description, name == dir name, no
// angle brackets, description within the per-skill and fleet byte budgets.
func TestSkillFrontmatter(t *testing.T) {
	entries, err := fs.ReadDir(builtinskills.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	seen := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		raw, err := fs.ReadFile(builtinskills.FS, dir+"/SKILL.md")
		if err != nil {
			t.Errorf("skill %q has no SKILL.md: %v", dir, err)
			continue
		}
		seen++
		name, desc := parseFrontmatter(t, dir, string(raw))
		if name != dir {
			t.Errorf("skill %q: frontmatter name %q must equal dir name", dir, name)
		}
		if desc == "" {
			t.Errorf("skill %q: empty description", dir)
		}
		if strings.ContainsAny(desc, "<>") {
			t.Errorf("skill %q: description contains angle brackets", dir)
		}
		if n := len(desc); n > maxDescBytes {
			t.Errorf("skill %q: description %d bytes exceeds cap %d", dir, n, maxDescBytes)
		}
		total += len(desc)
	}
	if seen == 0 {
		t.Fatal("no built-in skills found — test is broken")
	}
	if total > maxFleetDescBytes {
		t.Errorf("built-in description fleet %d bytes exceeds budget %d (keep new skills tight)", total, maxFleetDescBytes)
	}
}

// parseFrontmatter extracts the name and description from a SKILL.md's leading
// `---`-delimited YAML block using plain string ops (the frontmatter we ship is
// a flat set of single-line key: value pairs — no nesting, no multiline).
func parseFrontmatter(t *testing.T, dir, content string) (name, desc string) {
	t.Helper()
	if !strings.HasPrefix(content, "---\n") {
		t.Errorf("skill %q: missing leading frontmatter fence", dir)
		return "", ""
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Errorf("skill %q: unterminated frontmatter", dir)
		return "", ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "name":
			name = strings.TrimSpace(val)
		case "description":
			desc = strings.TrimSpace(val)
		}
	}
	return name, desc
}

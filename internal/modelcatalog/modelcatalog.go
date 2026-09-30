// Package modelcatalog reads an installed agent's own MODEL CATALOG, the model ids the agent
// knows with no network, from the files its pack declares (packdecl.Contribution.ModelCatalog):
// the one input `yolo check`'s currency warning compares yolo's model lists against
// (docs/design/model-lists-and-pickers.md MM-D16, MM-D19).
//
// It READS FILES AND NOTHING ELSE. It runs no program, reaches no network and writes nothing, so
// `yolo check` stays an observe verb, and an agent that is not installed is simply a catalog it
// could not read.
package modelcatalog

import (
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Bounds on what one catalog read may cost. pi 0.99.1's data files total under 1 MiB across 42
// files (MEASURED 2026-09-30), so both are far above any real catalog and exist only so a glob
// that matches the wrong tree cannot make `yolo check` read without end.
const (
	maxFiles     = 1024
	maxFileBytes = 32 << 20
)

// Catalog is what one read found.
type Catalog struct {
	// IDs is every id the read files name.
	IDs map[string]bool
	// Files is how many matched files parsed as JSON; 0 means the catalog could not be read,
	// whatever else was on disk.
	Files int
}

// Read reads the catalog under pkgDir, the installed package's directory: every file one of
// globs matches (slash-separated, relative to pkgDir, one path.Match pattern per segment), read as
// JSON, and every string an object holds under an `id` key, at any depth. A file that does not
// parse, or is larger than the bound, is skipped rather than failing the read: a catalog is
// what the files that do parse name.
func Read(pkgDir string, globs []string) Catalog {
	c := Catalog{IDs: map[string]bool{}}
	var files []string
	seen := map[string]bool{}
	for _, g := range globs {
		for _, f := range match(pkgDir, strings.Split(g, "/")) {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	sort.Strings(files)
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	for _, f := range files {
		doc, ok := readJSON(f)
		if !ok {
			continue
		}
		c.Files++
		collectIDs(doc, c.IDs)
	}
	return c
}

// Installed reports whether pkgDir holds an installed npm package: its package.json exists.
func Installed(pkgDir string) bool {
	st, err := os.Stat(filepath.Join(pkgDir, "package.json"))
	return err == nil && st.Mode().IsRegular()
}

// Version is the version the installed package's package.json names, "" when it names none or
// cannot be read, so a report never prints a sentinel it could mistake for one.
func Version(pkgDir string) string {
	doc, ok := readJSON(filepath.Join(pkgDir, "package.json"))
	if !ok {
		return ""
	}
	m, _ := doc.(map[string]any)
	v, _ := m["version"].(string)
	return v
}

// match returns the regular files under dir that the pattern segments name, in no set order. A
// segment is matched against directory entries with path.Match, so a pattern character in dir
// itself is never read as one.
func match(dir string, segs []string) []string {
	if len(segs) == 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		ok, err := path.Match(segs[0], e.Name())
		if err != nil || !ok {
			continue
		}
		p := filepath.Join(dir, e.Name())
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if len(segs) == 1 {
			if st.Mode().IsRegular() {
				out = append(out, p)
			}
			continue
		}
		if st.IsDir() {
			out = append(out, match(p, segs[1:])...)
		}
	}
	return out
}

// readJSON decodes one file, false when it cannot be read, is over the bound or is not JSON.
func readJSON(file string) (any, bool) {
	f, err := os.Open(file)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil || len(raw) > maxFileBytes {
		return nil, false
	}
	var doc any
	if json.Unmarshal(raw, &doc) != nil {
		return nil, false
	}
	return doc, true
}

// collectIDs adds every string held under an `id` key, in any object at any depth of v.
func collectIDs(v any, into map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if s, ok := child.(string); ok && k == "id" && s != "" {
				into[s] = true
				continue
			}
			collectIDs(child, into)
		}
	case []any:
		for _, child := range t {
			collectIDs(child, into)
		}
	}
}

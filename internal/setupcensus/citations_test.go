package setupcensus

// citations_test.go checks the census's reasons against the tree they cite. A reason exists so
// the next reader can go and re-check the code path that produces the disposition (census.go's
// "THE REASON IS FOR A MAINTAINER"), so a reason naming a file the path is not in, or a
// function that file does not define, sends that reader to the wrong place — the same defect as
// a stale `file:line` in a doc, which AGENTS.md calls "the exact place a reader stops checking".
//
// WHAT IS READ. A citation is a parenthesized list holding at least one Go file name, each
// optionally behind a package (`(backendcaps.go)`, `(entrypoint mcp.go)`,
// `(internal/cli/run seal.go, profilechannel.go)`); a file with no package of its own takes
// the one before it in the list, else the table's default (configkeys.go's header names
// internal/cli/run, kinds.go's internal/entrypoint), else the one package under internal/ that
// has a file of that name. Every cited file must exist. And where the word just before the
// parenthesis is a Go identifier — `gpuArgs (helpers.go)` — that identifier must be defined in
// one of the cited files, or, for a boot step's snake_case name, spelled there as a string. A
// function named inside the list (`(acMaterialize, helpers.go)`) is the one cited instead.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// repoRoot is the checkout this test file sits in.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path — cannot locate the repo root")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// citation matches a parenthesized list naming a .go file, with the word before it, if any.
var citation = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)?\s?\(([^()]*\.go[^()]*)\)`)

// citedFile matches one entry of a citation's list: an optional package, then a file name.
var citedFile = regexp.MustCompile(`^(?:([a-z][a-z0-9/]*) )?([a-z0-9_]+\.go)\b`)

// isCodeIdent reports whether a word reads as a Go identifier rather than prose: camelCase, an
// exported name with a lowercase letter after its first, or a snake_case boot step name.
func isCodeIdent(w string) bool {
	if w == "" {
		return false
	}
	if strings.Contains(w, "_") {
		return true
	}
	if hasInnerUpper(w) {
		return true
	}
	return w[0] >= 'A' && w[0] <= 'Z' && strings.ToLower(w[1:]) == w[1:] && len(w) > 3
}

// packageDir resolves a cited package spelling to its directory under the repo root.
func packageDir(root, pkg string) (string, bool) {
	for _, cand := range []string{pkg, filepath.Join("internal", pkg), filepath.Join("internal", "cli", pkg)} {
		if fi, err := os.Stat(filepath.Join(root, cand)); err == nil && fi.IsDir() {
			return cand, true
		}
	}
	return "", false
}

// findFile is the one directory under internal/ holding a file of this name, or "".
func findFile(root, name string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == name {
			rel, _ := filepath.Rel(root, filepath.Dir(p))
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// definesIdent reports whether Go source defines name, or, for a snake_case boot step name,
// spells it as a string literal.
func definesIdent(src, name string) bool {
	q := regexp.QuoteMeta(name)
	def := regexp.MustCompile(`(?m)^func (?:\([^)]*\) )?` + q + `\b|^(?:var|const|type) ` + q +
		`\b|^\t` + q + `\s+(?:=|[A-Za-z*\[(])`)
	if def.MatchString(src) {
		return true
	}
	return strings.Contains(name, "_") && strings.Contains(src, `"`+name+`"`)
}

// reasonsByTable is every cell's reason, keyed by the census path, with the table's default
// package: configkeys.go's reasons are internal/cli/run's unless they name another package,
// kinds.go's internal/entrypoint's.
func reasonsByTable() map[string]struct{ reason, defaultPkg string } {
	out := map[string]struct{ reason, defaultPkg string }{}
	eachCell(func(path string, s Setup, c Cell) {
		pkg := "internal/cli/run"
		if strings.HasPrefix(path, "pack kind ") {
			pkg = "internal/entrypoint"
		}
		out[path+" on "+s.String()] = struct{ reason, defaultPkg string }{c.Reason, pkg}
	})
	return out
}

func TestEveryCitedFileAndFunctionIsWhereTheReasonSays(t *testing.T) {
	root := repoRoot(t)
	sources := map[string]string{}
	read := func(rel string) (string, bool) {
		if s, ok := sources[rel]; ok {
			return s, true
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "", false
		}
		sources[rel] = string(b)
		return sources[rel], true
	}
	cells := reasonsByTable()
	where := make([]string, 0, len(cells))
	for w := range cells {
		where = append(where, w)
	}
	sort.Strings(where)
	for _, w := range where {
		c := cells[w]
		for _, m := range citation.FindAllStringSubmatch(c.reason, -1) {
			ident, list := m[1], m[2]
			pkg := ""
			var files, named []string
			for _, part := range strings.Split(list, ",") {
				fm := citedFile.FindStringSubmatch(strings.TrimSpace(part))
				if fm == nil {
					// A function named inside the list is the one cited, not the word before it:
					// `copied into wsState (acMaterialize, helpers.go)` cites acMaterialize. One
					// qualified by its own package (`macosuser.StageCtxCommands`) names where it
					// is, and is not looked for in the list's files.
					if first := strings.Fields(strings.TrimSpace(part)); len(first) > 0 &&
						!strings.Contains(first[0], ".") && isCodeIdent(strings.Trim(first[0], "`'")) {
						named = append(named, strings.Trim(first[0], "`'"))
					}
					continue
				}
				if fm[1] != "" {
					pkg = fm[1]
				}
				var rel string
				switch {
				case pkg != "":
					dir, ok := packageDir(root, pkg)
					if !ok {
						t.Errorf("%s cites package %q, which is no directory of this tree: %q", w, pkg, c.reason)
						continue
					}
					rel = filepath.Join(dir, fm[2])
				default:
					rel = filepath.Join(c.defaultPkg, fm[2])
					if _, ok := read(rel); !ok {
						dirs := findFile(root, fm[2])
						if len(dirs) != 1 {
							t.Errorf("%s cites %s with no package, and it is not in %s and is in %d "+
								"other packages %v: name the package (%q)", w, fm[2], c.defaultPkg,
								len(dirs), dirs, c.reason)
							continue
						}
						rel = filepath.Join(dirs[0], fm[2])
					}
				}
				if _, ok := read(rel); !ok {
					t.Errorf("%s cites %s, which does not exist: %q", w, rel, c.reason)
					continue
				}
				files = append(files, rel)
			}
			if len(files) == 0 {
				continue
			}
			if len(named) == 0 && isCodeIdent(ident) {
				named = []string{ident}
			}
			for _, id := range named {
				found := false
				for _, f := range files {
					if src, _ := read(f); definesIdent(src, id) {
						found = true
					}
				}
				if !found {
					t.Errorf("%s cites %s (%s), and %v does not define it; it is defined in %v: %q",
						w, id, list, files, definitionSites(root, id), c.reason)
				}
			}
		}
	}
}

// definitionSites names the files under internal/ defining ident, for the failure message.
func definitionSites(root, ident string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && definesIdent(string(b), ident) {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// The reader of a citation is the check's whole judgement, so its cases are pinned.
func TestTheCitationReaderReadsTheCensusSpellings(t *testing.T) {
	for reason, want := range map[string][2]string{
		"gpuArgs (helpers.go) passes":                 {"gpuArgs", "helpers.go"},
		"LoadMCPServers (entrypoint mcp.go) composes": {"LoadMCPServers", "entrypoint mcp.go"},
		"binds it (internal/cli/run seal.go, x.go)":   {"it", "internal/cli/run seal.go, x.go"},
	} {
		m := citation.FindStringSubmatch(reason)
		if m == nil || m[1] != want[0] || m[2] != want[1] {
			t.Errorf("citation.FindStringSubmatch(%q) = %q, want %q", reason, m, want)
		}
	}
	for w, want := range map[string]bool{
		"gpuArgs": true, "LoadMCPServers": true, "configure_host_files": true, "Run": false,
		"path": false, "PATH": false, "it": false, "BuildRunPlan": true,
	} {
		if got := isCodeIdent(w); got != want {
			t.Errorf("isCodeIdent(%q) = %v, want %v", w, got, want)
		}
	}
	src := "package x\n\nfunc (o *Options) gpuArgs(rt string) []string {}\nconst (\n\tKindX Kind = \"x\"\n)\n" +
		"var steps = []step{{name: \"configure_host_files\"}}\n"
	for name, want := range map[string]bool{"gpuArgs": true, "KindX": true, "configure_host_files": true,
		"kvmArgs": false} {
		if got := definesIdent(src, name); got != want {
			t.Errorf("definesIdent(src, %q) = %v, want %v", name, got, want)
		}
	}
}

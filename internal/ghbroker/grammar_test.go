package ghbroker

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// These tests pin the policy to the MEASURED grammar, in both directions: every path a
// policy table names must be a command gh has, and every file-shaped flag or positional
// in the grammar must have been reviewed into one of the lists. A regenerated grammar
// (tools/ghgrammar) that carries a new file flag fails here until someone decides whether
// it reads a host path (docs/design/boundary-broker.md BB-P2, BB-D28).

var fileishRE = regexp.MustCompile(`(?i)\bfile\b|\bpath\b|director|\bdisk\b|filename`)

func TestEveryFileShapedFlagIsReviewed(t *testing.T) {
	var missing []string
	for _, path := range sortedKeys(grammar) {
		for _, f := range grammar[path].flags {
			if !fileishRE.MatchString(f.placeholder) && !fileishRE.MatchString(f.desc) {
				continue
			}
			if _, ok := hostFileRule(path, f.long); ok {
				continue
			}
			if _, ok := notHostFileFlags[path+" --"+f.long]; ok {
				continue
			}
			if atFileFlags[path+" --"+f.long] {
				continue
			}
			missing = append(missing, path+" --"+f.long+" ("+f.desc+")")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("file-shaped flags reviewed into neither hostFileFlags nor notHostFileFlags:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

var fileishUsageRE = regexp.MustCompile(`(?i)file|path|dir|pattern|repositor|source|dest|key`)

func TestEveryFileShapedPositionalIsReviewed(t *testing.T) {
	var missing []string
	for _, path := range sortedKeys(grammar) {
		c := grammar[path]
		usage := strings.TrimPrefix(c.usage, "gh "+path)
		if !fileishUsageRE.MatchString(usage) {
			continue
		}
		if _, ok := hostFilePositionals[path]; ok {
			continue
		}
		if _, ok := notHostFilePositionals[path]; ok {
			continue
		}
		missing = append(missing, path+" "+usage)
	}
	if len(missing) > 0 {
		t.Fatalf("usage lines naming a file, path or directory, reviewed into neither "+
			"hostFilePositionals nor notHostFilePositionals:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestPolicyNamesOnlyRealCommands(t *testing.T) {
	check := func(table, path string) {
		t.Helper()
		if grammar[path] == nil {
			t.Errorf("%s names %q, which gh %s does not have", table, path, grammarVersion)
		}
	}
	for _, r := range refusedCommands {
		check("refusedCommands", r.path)
		for _, e := range r.except {
			check("refusedCommands.except", e)
		}
		if r.unless != "" && grammar[r.path].flagByLong(r.unless) == nil {
			t.Errorf("refusedCommands %q unless --%s: no such flag", r.path, r.unless)
		}
	}
	for _, path := range sortedKeys(readOnly) {
		check("readOnly", path)
		if isGroup(path) {
			t.Errorf("readOnly names the group %q, which never runs", path)
		}
		for _, f := range readOnly[path].accountFlags {
			if grammar[path].flagByLong(f) == nil {
				t.Errorf("readOnly %q account flag --%s: no such flag", path, f)
			}
		}
		for _, f := range readOnly[path].require {
			if grammar[path].flagByLong(f) == nil {
				t.Errorf("readOnly %q requires --%s: no such flag", path, f)
			}
		}
	}
	for _, path := range accountWide {
		check("accountWide", path)
	}
	for _, h := range hostFileFlags {
		if h.path == "*" {
			continue
		}
		check("hostFileFlags", h.path)
		if grammar[h.path] != nil && grammar[h.path].flagByLong(h.long) == nil {
			t.Errorf("hostFileFlags %q --%s: no such flag", h.path, h.long)
		}
	}
	for key := range atFileFlags {
		path, long, _ := strings.Cut(key, " --")
		check("atFileFlags", path)
		if grammar[path] != nil && grammar[path].flagByLong(long) == nil {
			t.Errorf("atFileFlags %q: no such flag", key)
		}
	}
	for path := range hostFilePositionals {
		check("hostFilePositionals", path)
	}
}

// Every read-only command that names a repository must take -R, a repository positional,
// or a URL, so the canonical argv can say which repository it reads (§4.1: "always
// explicit"). A command the broker could only point at a repository through its cwd would
// read the broker's empty directory instead.
func TestEveryRepositoryReadNamesItsRepository(t *testing.T) {
	for _, path := range sortedKeys(readOnly) {
		r := readOnly[path]
		if r.scope != scopeRepo || path == "api" || strings.HasPrefix(path, "search ") {
			continue
		}
		c := grammar[path]
		if c.flagByLong("repo") == nil && !repoPositional(c) {
			t.Errorf("read-only %q has no -R and no repository positional", path)
		}
	}
}

// The alias index resolves only gh's own built-in aliases.
func TestAliasIndexResolvesBuiltinAliases(t *testing.T) {
	for alias, want := range map[string]string{
		"pr ls":         "pr list",
		"cs ls":         "codespace list",
		"rs ls":         "ruleset list",
		"ext uninstall": "extension remove",
		"agent list":    "agent-task list",
	} {
		words := strings.Fields(alias)
		path := ""
		for _, w := range words {
			path = childPath(path, w)
			if path == "" {
				break
			}
		}
		if path != want {
			t.Errorf("%q resolved to %q, want %q", alias, path, want)
		}
	}
	if childPath("pr", "vw") != "" {
		t.Errorf("a word that is no built-in alias must not resolve")
	}
}

// The grammar carries every flag the design's rules name, so a rule cannot silently stop
// applying because a flag was renamed upstream.
func TestGrammarCarriesTheFlagsTheRulesName(t *testing.T) {
	want := map[string][]string{
		"pr view":        {"jq", "template", "web", "repo", "comments"},
		"api":            {"method", "field", "raw-field", "header", "input", "hostname", "verbose"},
		"auth status":    {"show-token", "hostname"},
		"repo read-file": {"output", "clobber"},
		"issue create":   {"template", "editor"},
	}
	var missing []string
	for _, path := range sortedKeys(want) {
		for _, f := range want[path] {
			if grammar[path] == nil || grammar[path].flagByLong(f) == nil {
				missing = append(missing, path+" --"+f)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("grammar lacks %v", missing)
	}
}

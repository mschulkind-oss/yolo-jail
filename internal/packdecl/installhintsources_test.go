package packdecl

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// EVERY INSTALL HINT THE REPOSITORY SHIPS OR TEACHES RECORDS WHERE ITS NAME CAME FROM. The happy
// path principle's rule 3 (docs/reference/happy-path-principle.md#the-rules): advice must be
// specific and verified, every platform-specific entry records its source, and a test rejects any
// entry that doesn't. A package name is advice the user runs as root, and a wrong one (Debian's
// `fd-find` installs no `fd` on PATH) is a next step that leads nowhere.
//
// The record is a comment, so this reads each manifest's RAW text, not its decode: the `//` lines
// directly above the line that opens a contribution, with no blank line between, must hold one
// SOURCE LINE per manager key its install_hints declares, `<key>: …` with an https URL and every
// package the hint names, as packs/guardrails/pack.json writes them. A user's own pack is not
// read: the rule is the repository's, checked against what it ships and what it teaches.
//
// The test does not fetch the pages, so it proves a source is recorded, not that it still says
// what it said: re-checking a name is re-reading the page its line names.
func TestEveryShippedAndExampleInstallHintNamesItsSource(t *testing.T) {
	manifests := shippedAndExampleManifests(t)
	total := 0
	read := map[string]int{}
	for _, path := range manifests {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		problems, hinted, err := installHintSourceProblems(raw)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		total += hinted
		read[filepath.Base(filepath.Dir(path))] += hinted
		for _, p := range problems {
			t.Errorf("%s: %s", path, p)
		}
	}
	// The control: a gate that found no hint passes for free. The guardrails pack is named
	// because its comments are the shape every other hint copies, and the fzf example because
	// it is the hinted pack users copy.
	if total == 0 || read["guardrails"] != 2 || read["claude-fzf-pack"] != 2 {
		t.Fatalf("read %d hinted contributions (guardrails %d, claude-fzf-pack %d, want 2 each) "+
			"across %d manifests: the gate is not reading the tree it exists for",
			total, read["guardrails"], read["claude-fzf-pack"], len(manifests))
	}
}

// The checker itself, on manifests built to break it one way each, so a deleted source line, a
// line for the wrong key, a line that is not directly above, or a URL that is not https is a
// failure the suite keeps, rather than one checked once by hand.
func TestInstallHintSourceProblems(t *testing.T) {
	const good = `{
  "name": "x",
  "contributes": [
    // Sources, checked 2026-10-02:
    //   brew: https://formulae.brew.sh/api/formula/fd.json
    //   apt: https://packages.debian.org/trixie/amd64/fd-find/filelist
    { "kind": "requires", "bin": "fd", "install_hints": {"brew": "fd", "apt": "fd-find && sudo ln -sf /usr/lib/cargo/bin/fd /usr/local/bin/fd"} },
    { "kind": "requires", "bin": "rg" },
    // brew-cask: https://formulae.brew.sh/api/cask/claude-code.json
    {
      "bin": "claude",
      "install_hints": {
        "brew-cask": "claude-code"
      },
      "kind": "requires"
    }
  ]
}`
	cases := []struct {
		name string
		edit func(string) string
		want []string // substrings of the one problem; nil wants none
	}{
		{"every key sourced", func(s string) string { return s }, nil},
		{"a deleted source line", func(s string) string {
			return strings.Replace(s, "    //   apt: https://packages.debian.org/trixie/amd64/fd-find/filelist\n", "", 1)
		}, []string{`contributes[0]`, `bin "fd"`, `install_hints "apt"`, "// apt: "}},
		{"a blank line between the comment and the contribution", func(s string) string {
			return strings.Replace(s, "filelist\n    {", "filelist\n\n    {", 1)
		}, []string{`install_hints "apt"`, `install_hints "brew"`}},
		{"a brew line does not source brew-cask", func(s string) string {
			return strings.Replace(s, `{"brew": "fd", "apt"`, `{"brew": "fd", "brew-cask": "fd", "apt"`, 1)
		}, []string{`contributes[0]`, `install_hints "brew-cask"`}},
		{"a brew-cask line does not source brew", func(s string) string {
			return strings.Replace(s, `"brew-cask": "claude-code"`, `"brew-cask": "claude-code", "brew": "claude-code"`, 1)
		}, []string{`contributes[2]`, `install_hints "brew"`}},
		{"an http URL", func(s string) string {
			return strings.Replace(s, "https://formulae.brew.sh/api/cask/claude-code.json", "http://formulae.brew.sh/api/cask/claude-code.json", 1)
		}, []string{`contributes[2]`, `install_hints "brew-cask"`}},
		{"no URL at all", func(s string) string {
			return strings.Replace(s, "https://formulae.brew.sh/api/formula/fd.json", "Homebrew's formula", 1)
		}, []string{`install_hints "brew"`}},
		{"a line for another package", func(s string) string {
			return strings.Replace(s, "api/formula/fd.json", "api/formula/ripgrep.json", 1)
		}, []string{`install_hints "brew"`, `"fd"`}},
		{"the comment above another contribution", func(s string) string {
			return strings.Replace(s, "    // brew-cask: https://formulae.brew.sh/api/cask/claude-code.json\n", "", 1)
		}, []string{`contributes[2]`, `install_hints "brew-cask"`}},
		{"a key mentioned mid-line", func(s string) string {
			return strings.Replace(s, "//   apt: https", "//   see apt: https", 1)
		}, []string{`install_hints "apt"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.edit(good)
			if tc.want != nil && src == good {
				t.Fatal("the edit changed nothing: the fixture moved, so the case tests nothing")
			}
			problems, hinted, err := installHintSourceProblems([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if hinted != 2 {
				t.Errorf("read %d hinted contributions, want 2", hinted)
			}
			if tc.want == nil {
				if len(problems) != 0 {
					t.Errorf("want no problem, got %v", problems)
				}
				return
			}
			// One problem per unsourced key; every wanted substring is in one of them.
			joined := strings.Join(problems, "\n")
			if len(problems) == 0 {
				t.Fatalf("want a problem naming %v, got none", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("no problem names %s:\n%s", w, joined)
				}
			}
		})
	}
}

// installHintSourceProblems reads one manifest's RAW text and returns a problem for each manager
// key of an install_hints contribution that has no source line in the contribution's comment
// block: the `//` lines directly above the line that opens it, with no blank line between. A
// source line reads `<key>: …` once the `//` and the spaces around it are trimmed, and holds an
// https:// URL and every package of the hint's package part (the names before any ` && ` step).
// It also returns how many hinted contributions it read.
func installHintSourceProblems(raw []byte) (problems []string, hinted int, err error) {
	m, probs := Decode(raw)
	if m == nil {
		return nil, 0, fmt.Errorf("does not decode: %v", probs)
	}
	opens, err := contributionOpenLines(raw)
	if err != nil {
		return nil, 0, err
	}
	if len(opens) != len(m.Contributes) {
		return nil, 0, fmt.Errorf("found %d contributions in the text and %d in the decode: "+
			"the scan that finds each one's comment is wrong", len(opens), len(m.Contributes))
	}
	lines := strings.Split(string(raw), "\n")
	for i, c := range m.Contributes {
		if len(c.InstallHints) == 0 {
			continue
		}
		hinted++
		block := commentBlockAbove(lines, opens[i])
		keys := make([]string, 0, len(c.InstallHints))
		for k := range c.InstallHints {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, mgr := range keys {
			pkgs, _, _ := strings.Cut(c.InstallHints[mgr], hintStepSeparator)
			if !hasSourceLine(block, mgr, strings.Fields(pkgs)) {
				problems = append(problems, fmt.Sprintf("contributes[%d] (line %d, bin %q): "+
					"install_hints %q (%q) records no source: add `// %s: <https URL of the "+
					"page that lists it>`, naming the package, directly above the contribution",
					i, opens[i], c.Bin, mgr, strings.TrimSpace(pkgs), mgr))
			}
		}
	}
	return problems, hinted, nil
}

// hasSourceLine reports whether a line of block is mgr's source line for pkgs.
func hasSourceLine(block []string, mgr string, pkgs []string) bool {
	for _, l := range block {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "//"))
		if !strings.HasPrefix(text, mgr+":") || !strings.Contains(text, "https://") {
			continue
		}
		named := true
		for _, p := range pkgs {
			named = named && strings.Contains(text, p)
		}
		if named {
			return true
		}
	}
	return false
}

// commentBlockAbove is the run of `//` lines that ends on the line before open (1-based).
func commentBlockAbove(lines []string, open int) []string {
	start := open - 1 // the 0-based index of the opening line
	for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "//") {
		start--
	}
	return lines[start : open-1]
}

// contributionOpenLines returns the 1-based line on which each element of the manifest's
// top-level `contributes` array opens. No decoder reports where a value sits, so it walks the
// JSON5 text itself: comments of both forms, strings in either quote with backslash escapes,
// and bare keys.
func contributionOpenLines(raw []byte) ([]int, error) {
	var (
		stack     []bool // one entry per open { or [, true for the contributes array
		lastToken string // the latest string or bare word in the top-level object: a key before [
		line      = 1
		opens     []int
	)
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == '\n':
			line++
		case c == '/' && i+1 < len(raw) && raw[i+1] == '/':
			for i+1 < len(raw) && raw[i+1] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(raw) && raw[i+1] == '*':
			end := bytes.Index(raw[i+2:], []byte("*/"))
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated /* comment", line)
			}
			line += bytes.Count(raw[i:i+2+end], []byte("\n"))
			i += 2 + end + 1
		case c == '"' || c == '\'':
			j := i + 1
			for ; j < len(raw) && raw[j] != c; j++ {
				if raw[j] == '\\' {
					j++
				}
			}
			if j >= len(raw) {
				return nil, fmt.Errorf("line %d: unterminated string", line)
			}
			line += bytes.Count(raw[i:j], []byte("\n"))
			if len(stack) == 1 {
				lastToken = string(raw[i+1 : j])
			}
			i = j
		case c == '{' || c == '[':
			if c == '{' && len(stack) == 2 && stack[1] {
				opens = append(opens, line)
			}
			stack = append(stack, c == '[' && len(stack) == 1 && lastToken == "contributes")
		case c == '}' || c == ']':
			if len(stack) == 0 {
				return nil, fmt.Errorf("line %d: unbalanced %q", line, c)
			}
			stack = stack[:len(stack)-1]
		case c == '_' || c == '$' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9'):
			j := i
			for j < len(raw) && (raw[j] == '_' || raw[j] == '$' || ('a' <= raw[j] && raw[j] <= 'z') ||
				('A' <= raw[j] && raw[j] <= 'Z') || ('0' <= raw[j] && raw[j] <= '9')) {
				j++
			}
			if len(stack) == 1 {
				lastToken = string(raw[i:j])
			}
			i = j - 1
		}
	}
	return opens, nil
}

package packload

// duplicateloads.go is the DUPLICATE-LOAD LINT (a term coined here) for a patched extension
// (docs/design/patched-extensions.md PPX-D34): one list that loads an extension's built tree from
// `~/<into>` and loads the same package again from a remote entry.
//
// pi tells packages apart by source, and names a local folder `local:<path>`, so an extension's old
// `git:` entry left beside the tree's entry is a second package, loaded twice (the patch-series
// guide's warning, which nothing checked). Telling the two apart in general needs pi's whole
// package-source grammar, which core does not read (PPX-D10); the lint asks the narrow question
// instead, by FINAL NAME, as the pi pack's own subagents render matches a package
// (packs/pi/pack.json, `whenListed.matches`):
//
//   - a REMOTE entry is a string, or an object's `source`, that opens with `git:` or `npm:` or
//     names a URL (`<scheme>://`); a local path is never one, since the agent loads it from where it
//     points;
//   - its final name is its last path segment, without an `@<ref>` or `@<version>` and a `.git`,
//     after an npm scope;
//   - it duplicates the tree when that name is the extension's own name (the last segment of
//     `into`) or its upstream's (the source's subdirectory, else its repository).
//
// THE SAME LIST is one surface and path, across every list body of the pack whose notches meet: a
// `config-list` reaches every notch, so it meets either posture's list, and the autonomous and
// guarded postures never meet each other. A list holds the tree when an entry loads it, as an owner
// reads one (loadsTree, PPX-D36): `~/<into>`, or a path inside it.
//
// A warning, never a failure, at `yolo pack lint`: the agent still starts, with the extension
// loaded twice.

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// LintDuplicateLoads is the duplicate-load lint for every patched extension of p: one warning per
// remote entry that loads an extension a second time, naming the entry to drop.
func LintDuplicateLoads(p *Pack) []string {
	if p == nil || p.Decl == nil {
		return nil
	}
	lists := p.Decl.ListContributions()
	var out []string
	seen := map[string]bool{}
	for _, c := range p.Decl.Contributions() {
		if !c.IsPatchedExtension() || c.ExtensionName() == "" {
			continue
		}
		tree := TreeListEntry(c.Into)
		names := map[string]bool{c.ExtensionName(): true}
		if n := upstreamName(c.Source); n != "" {
			names[n] = true
		}
		for _, l := range lists {
			if !holdsTree(l.Add, tree) {
				continue
			}
			for _, m := range lists {
				if m.Surface != l.Surface || m.Path != l.Path || !posturesMeet(l.Posture, m.Posture) {
					continue
				}
				for _, src := range listSources(m.Add) {
					name, ok := remotePackageName(src)
					key := c.Into + "\x00" + l.Surface + "\x00" + l.Path + "\x00" + src
					if !ok || !names[name] || seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, fmt.Sprintf("pack %s: the %slist at %s on %s holds both %q, the build of "+
						"extension %s/%s, and %q, the same package by its name, so the agent loads it twice — "+
						"drop %q from the list", p.Name, listPostures(l.Posture, m.Posture), l.Path, l.Surface,
						tree, p.Name, c.ExtensionName(), src, src))
				}
			}
		}
	}
	return out
}

// holdsTree reports whether a list body's `add` array holds an entry that loads tree, `~/<into>`:
// the entry itself, or a path inside it, by the one rule the owner and the lint read (loadsTree,
// PPX-D36), so `~/<into>/../x` holds no part of it.
func holdsTree(add json.RawMessage, tree string) bool {
	for _, src := range listSources(add) {
		if loadsTree(src, tree) {
			return true
		}
	}
	return false
}

// listSources is each entry of a list body's `add` array as a package source: a string as it is,
// an object's `source` member, nothing for anything else.
func listSources(add json.RawMessage) []string {
	var entries []any
	if err := json.Unmarshal(add, &entries); err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		switch v := e.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			if s, ok := v["source"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// posturesMeet reports whether two list bodies contribute at a notch they share: a `config-list`
// ("") at every notch, a posture's list at its own.
func posturesMeet(a, b packdecl.Posture) bool {
	return a == "" || b == "" || a == b
}

// listPostures names the posture of the list two bodies share, with a trailing space, when either is
// a posture's list; "" for two config-lists, which share every notch.
func listPostures(a, b packdecl.Posture) string {
	switch {
	case a != "":
		return string(a) + " posture's "
	case b != "":
		return string(b) + " posture's "
	}
	return ""
}

// remotePackageName is the final name of a remote package source, and whether src is one: a `git:`
// or `npm:` entry, or a URL. See the file doc.
func remotePackageName(src string) (string, bool) {
	src = strings.TrimSpace(src)
	switch {
	case strings.HasPrefix(src, "npm:"):
		spec := strings.TrimPrefix(src, "npm:")
		if scoped, ok := strings.CutPrefix(spec, "@"); ok {
			_, spec, ok = strings.Cut(scoped, "/")
			if !ok {
				return "", false
			}
		}
		name, _, _ := strings.Cut(spec, "@")
		return name, name != ""
	case strings.HasPrefix(src, "git:"), strings.Contains(src, "://"):
		rest := strings.TrimSpace(strings.TrimPrefix(src, "git:"))
		// The path after the host, whose first "@" opens the ref, as pi splits it: a ref may hold a
		// slash, so the ref goes before the last segment is taken.
		if _, after, ok := strings.Cut(rest, "://"); ok {
			_, rest, _ = strings.Cut(after, "/")
		} else if host, after, ok := strings.Cut(rest, ":"); ok && strings.HasPrefix(host, "git@") && !strings.Contains(host, "/") {
			rest = after
		} else {
			_, rest, _ = strings.Cut(rest, "/")
		}
		rest, _, _ = strings.Cut(rest, "@")
		rest, _, _ = strings.Cut(rest, "?")
		rest, _, _ = strings.Cut(rest, "#")
		rest = strings.TrimRight(rest, "/")
		last := strings.TrimSuffix(rest[strings.LastIndex(rest, "/")+1:], ".git")
		return last, last != ""
	}
	return "", false
}

// upstreamName is the final name of a patched extension's upstream: its subdirectory's last segment,
// else its repository's, "" when the source does not parse.
func upstreamName(source string) string {
	a, err := packsrc.Parse(source)
	if err != nil || a.IsLocal() {
		return ""
	}
	if a.Path != "" {
		return path.Base(a.Path)
	}
	repo := strings.TrimRight(a.Repo, "/")
	return strings.TrimSuffix(repo[strings.LastIndexAny(repo, "/:")+1:], ".git")
}

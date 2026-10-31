package configmigrate

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A legacy `packs` entry may be a filesystem path ("~/packs/mine", "/abs/mine", file://…) or a git
// address ("git+https://host/owner/repo?ref=main", with "//sub" for a subdirectory). The TOML
// `packs` list takes neither: a pack yolo does not ship is named by its source (OQ-RC15,
// config-user.md §4), so the conversion writes the spellings that ruling gives (RC-D43):
//
//   - a directory under ~/.config/yolo-jail/packs is local/<name>;
//   - any other directory's parent becomes a `pack_sources` entry and the pack <source>/<name>;
//   - a repository that is one pack is { git = URL, ref = REF };
//   - a one-segment subdirectory of a repository makes the repository a `pack_sources` entry
//     and the pack <source>/<subdir>.
//
// The pack's directory is the one the old entry named. Its handle is the new spelling, which is
// also the name yolo uses for it from then on (OQ-RC15). The source names are given once, over the
// whole merged user config, so two files naming one directory write the same source.

// packSource is one `pack_sources` entry a conversion writes.
type packSource struct {
	key   string // what identifies it: "dir:<parent as written>" or "git:<repo>@<ref>"
	value any    // its TOML value: the directory as written, or { git, ref }
}

// convertedPack is one legacy entry's new spelling, or why it has none.
type convertedPack struct {
	handle any         // a string handle or a { git, ref } table
	source *packSource // the source it needs, nil for none
	reason string      // non-empty when it does not convert
}

// classifyPack reads one legacy `packs` entry. A bare name (a shipped pack) is returned unchanged.
func classifyPack(entry any) convertedPack {
	text, ok := entry.(string)
	if !ok {
		obj, isMap := entry.(*jsonx.OrderedMap)
		if !isMap {
			return convertedPack{reason: "a pack entry is a source string or an object"}
		}
		for _, k := range obj.Keys() {
			if k != "source" {
				return convertedPack{reason: "a pack entry's `" + k + "` has no TOML spelling (a pack is named by its source, OQ-RC15); remove it from the old file, or keep the old file for now"}
			}
		}
		src, _ := obj.Get("source")
		if text, ok = src.(string); !ok {
			return convertedPack{reason: "a pack entry's `source` must be a string"}
		}
	}
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "/") && !strings.Contains(text, ":") {
		return convertedPack{handle: text}
	}
	if strings.HasPrefix(text, "git+") {
		return classifyGitPack(text)
	}
	dir := text
	if rest, isFile := strings.CutPrefix(text, "file://"); isFile {
		dir = rest
	}
	if !strings.HasPrefix(dir, "~/") && !strings.HasPrefix(dir, "/") {
		return convertedPack{reason: "a pack path must be absolute or start with ~/; write it so in the old file, then run `yolo config migrate` again"}
	}
	dir = strings.TrimRight(dir, "/")
	parent, name := path.Dir(dir), path.Base(dir)
	if !validSegment(name) {
		return convertedPack{reason: fmt.Sprintf("the pack directory %q has no name a TOML handle can carry", name)}
	}
	if expandHome(parent) == filepath.Join(paths.Home(), ".config", "yolo-jail", "packs") {
		return convertedPack{handle: "local/" + name}
	}
	if parent == "~" || parent == "/" {
		return convertedPack{reason: "a pack directory directly under ~ or / has no source a TOML handle can name; move it into a directory of packs, or keep the old file for now"}
	}
	return convertedPack{handle: name, source: &packSource{key: "dir:" + parent, value: parent}}
}

func classifyGitPack(text string) convertedPack {
	addr, err := packsrc.Parse(text)
	if err != nil {
		return convertedPack{reason: err.Error()}
	}
	table := jsonx.NewOrderedMap()
	table.Set("git", addr.Repo)
	if addr.Ref != "" {
		table.Set("ref", addr.Ref)
	}
	if addr.Path == "" {
		if !ownerRepoShaped(addr.Repo) {
			return convertedPack{reason: "a repository that is one pack is named owner/repository from its URL, and " + addr.Repo + " has no owner/repository path; keep the old file for now"}
		}
		return convertedPack{handle: table}
	}
	if strings.Contains(addr.Path, "/") || !validSegment(addr.Path) {
		return convertedPack{reason: "a pack in a nested subdirectory (" + addr.Path + ") has no TOML handle: a source is a repository of <pack>/ directories; keep the old file for now"}
	}
	return convertedPack{handle: addr.Path, source: &packSource{key: "git:" + addr.Repo + "@" + addr.Ref, value: table}}
}

func ownerRepoShaped(repo string) bool {
	_, rest, ok := strings.Cut(repo, "://")
	if !ok {
		return false
	}
	_, p, ok := strings.Cut(rest, "/")
	if !ok {
		return false
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	return len(parts) == 2 && validSegment(parts[0]) && validSegment(parts[1])
}

func validSegment(s string) bool {
	return s != "" && s != "." && s != ".." && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\\:/?#")
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(paths.Home(), rest)
	}
	if p == "~" {
		return paths.Home()
	}
	return filepath.Clean(p)
}

// sourceNames gives each source the packs list needs a name, in the order the list first needs it:
// the base name of its directory or repository, "packs" when that cannot be a source name, and a
// -2, -3 … suffix when an earlier source took it.
func sourceNames(list []any) map[string]string {
	names := map[string]string{}
	taken := map[string]bool{"local": true, "yolo": true}
	for _, entry := range list {
		c := classifyPack(entry)
		if c.source == nil || names[c.source.key] != "" {
			continue
		}
		base := path.Base(strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(c.source.key, "dir:"), "git:"), "/"))
		if strings.HasPrefix(c.source.key, "git:") {
			repo, _, _ := strings.Cut(strings.TrimPrefix(c.source.key, "git:"), "@")
			base = path.Base(repo)
		}
		if !validSegment(base) || taken[base] {
			if !validSegment(base) {
				base = "packs"
			}
		}
		name := base
		for i := 2; taken[name]; i++ {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		taken[name] = true
		names[c.source.key] = name
	}
	return names
}

// lowerPacks converts one file's `packs` list (RC-D43), keeping every entry in its place. An entry
// with no TOML spelling is reported; the equality check's old side keeps it at its old spelling,
// so the proof shows it and the migration writes nothing.
func lowerPacks(value any, ctx loweringContext, place func([]string, any, []string) bool, skip func([]string, any, string), report func([]string, string)) {
	list, ok := value.([]any)
	if !ok {
		skip([]string{"packs"}, value, "packs must be a list")
		return
	}
	names := ctx.view().sources
	if names == nil {
		names = sourceNames(list)
	}
	out := make([]any, 0, len(list))
	for i, entry := range list {
		at := []string{fmt.Sprintf("packs[%d]", i)}
		c := classifyPack(entry)
		if c.reason != "" {
			report(at, c.reason)
			if ctx.keepUnconverted {
				out = append(out, entry)
			}
			continue
		}
		if c.source != nil {
			name := names[c.source.key]
			if name == "" {
				name = sourceNames([]any{entry})[c.source.key]
			}
			where := []string{"pack_sources", name}
			place(where, c.source.value, []string{"packs"})
			c.handle = name + "/" + c.handle.(string)
		}
		out = append(out, c.handle)
	}
	if !place([]string{"packs"}, out, []string{"packs"}) {
		skip([]string{"packs"}, value, "packs is already written in this file")
	}
}

package config

// readablepackslocalkey.go validates the `local` key of a `packs` entry: the user's clone of a branch-following
// git pack (docs/design/local-pack-clones.md §6.1, LPC-D7). The key is TOML-only (the legacy JSONC entry form
// keeps refusing it as an unknown key) and user-scope by construction, because `packs` is refused in a workspace
// config. packsrc.ParsePackReference does not know the key, so it is read and removed here before the rest of the
// entry goes there. A missing clone is NOT an error (LPC-D8) and is not checked here; only a malformed value is.

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const packLocalKey = "local"

// withoutKey is an Object that hides one key of the entry it wraps.
type withoutKey struct {
	packsrc.Object
	hidden string
}

func (w withoutKey) Keys() []string {
	keys := make([]string, 0, len(w.Object.Keys()))
	for _, k := range w.Object.Keys() {
		if k != w.hidden {
			keys = append(keys, k)
		}
	}
	return keys
}

func (w withoutKey) Get(key string) (any, bool) {
	if key == w.hidden {
		return nil, false
	}
	return w.Object.Get(key)
}

// splitLocalKey returns the entry without its `local` key and the validated, expanded, absolute path ("" when
// the key is absent). The error names the entry and the verb that fixes it.
func splitLocalKey(raw any, index int) (any, string, error) {
	obj, ok := raw.(packsrc.Object)
	if !ok {
		return raw, "", nil
	}
	rawLocal, has := obj.Get(packLocalKey)
	if !has {
		return raw, "", nil
	}
	entryPath := "packs[" + strconv.Itoa(index) + "]"
	unset := func(key string) string {
		return "`" + shquote.Join([]string{"yolo", "config", "unset", entryPath + "." + key, "--user"}) + "`"
	}
	text, isString := rawLocal.(string)
	switch {
	case !isString:
		return nil, "", fmt.Errorf("local: expected a string path to your clone; fix it with %s",
			packsrc.ConfigCommand("set", entryPath+".local", "~/src/<repo>"))
	case text == "":
		return nil, "", fmt.Errorf("local: expected a non-empty path to your clone; fix it with %s, or remove it with %s",
			packsrc.ConfigCommand("set", entryPath+".local", "~/src/<repo>"), unset("local"))
	}
	expanded := text
	if text == "~" || strings.HasPrefix(text, "~/") {
		expanded = strings.TrimRight(paths.Home(), "/") + text[1:]
	}
	if !filepath.IsAbs(expanded) {
		return nil, "", fmt.Errorf("local %q is not absolute: write an absolute path or one starting with ~/; fix it with %s",
			text, packsrc.ConfigCommand("set", entryPath+".local", "~/"+strings.TrimPrefix(text, "./")))
	}
	if _, git := obj.Get("git"); !git {
		return nil, "", fmt.Errorf("local belongs only on a { git = … } entry, not on { pack = … }; remove it with %s", unset("local"))
	}
	ref, hasRef := obj.Get("ref")
	refText, _ := ref.(string)
	if !hasRef || isFullCommitRef(refText) || strings.HasPrefix(refText, "refs/tags/") {
		return nil, "", fmt.Errorf("local follows a branch: write ref = \"main\" (your branch) with %s, or remove local with %s",
			packsrc.ConfigCommand("set", entryPath+".ref", "main"), unset("local"))
	}
	if _, skills := obj.Get("skills"); skills {
		return nil, "", fmt.Errorf("local cannot be combined with skills (a skills import binds to a commit pin, and a working tree has none): "+
			"drop skills with %s, or use a file:// source", unset("skills"))
	}
	return withoutKey{Object: obj, hidden: packLocalKey}, expanded, nil
}

// isFullCommitRef reports a 40-hex ref, the spelling of a commit pin.
func isFullCommitRef(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, r := range ref {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

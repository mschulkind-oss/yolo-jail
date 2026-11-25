package configmigrate

import "strings"

// Why is, in words, what makes a plan rewrite this file in place when it is already TOML: the
// include entries it respells, the keys it converts, the ceiling lines it gains, the renamed
// packs it respells. "" for a file converted to its TOML twin. The launch's offer says it before
// its question, so a yes covers every file it will move (RC-D41, RC-D35).
func (t Target) Why() string {
	if t.File == nil || t.File.Role != RoleRespell {
		return ""
	}
	var reasons []string
	if len(t.Respelled) > 0 {
		reasons = append(reasons, "its include_if_found "+plural(len(t.Respelled), "entry", "entries")+" naming "+
			strings.Join(t.Respelled, ", ")+" now name the converted "+plural(len(t.Respelled), "file", "files"))
	}
	if len(t.Ceiling) > 0 {
		reasons = append(reasons, "it gains "+strings.Join(t.Ceiling, ", ")+", so the converted workspace's legacy switches keep their meaning")
	}
	if len(t.Rewrote) > 0 {
		reasons = append(reasons, "it converts "+strings.Join(t.Rewrote, "; "))
	}
	if len(t.File.packRenames) > 0 {
		reasons = append(reasons, "its requests naming renamed packs are respelled")
	}
	if len(reasons) == 0 {
		return "it is rewritten in place"
	}
	return strings.Join(reasons, "; and ")
}

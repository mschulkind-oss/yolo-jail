package jailcontent

// shippedprefix.go delivers yolo's built-in skills under the user's `shipped_skill_prefix`
// (docs/design/pack-naming.md §3.7, PN-D13): with it set, configuring-the-jail is delivered as
// `<prefix>-configuring-the-jail`, its SKILL.md says so, and every reference to a built-in skill —
// in the suite's own bodies and in the briefing — names the delivered skill. Unset, every name is
// exactly what it always was. Shipped PACKS take the same prefix through hostskills.PackLayer.

import (
	"bytes"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
)

// shippedSkillPrefix is the user's shipped_skill_prefix: a variable so a test of a layer below the
// config can say what the user configured.
var shippedSkillPrefix = config.ShippedSkillPrefix

// builtinSkillBareNames are the built-in suite's skills by the names yolo wrote them under.
var builtinSkillBareNames = []string{"configuring-the-jail", "diagnosing-the-jail", mechanicsSkill}

// BuiltinSkillBareNames is the built-in suite's skills by the names yolo wrote them under, before
// any shipped_skill_prefix.
func BuiltinSkillBareNames() []string { return append([]string(nil), builtinSkillBareNames...) }

// mechanicsSkill is the generated skill whose rendered copy overrides a pack's of the same name.
const mechanicsSkill = "jail-mechanics"

// BuiltinSkillName is the name a built-in skill is delivered under: `<prefix>-<name>`, or name
// itself when prefix is "".
func BuiltinSkillName(prefix, name string) string { return packname.Delivered(prefix, name) }

// PrefixBuiltinSkillFiles is files, a built-in suite keyed by slash path under its skill
// directories, delivered under prefix: each built-in skill's directory and its SKILL.md `name` are
// BuiltinSkillName, and each `**<skill>**` reference to a built-in skill in any file names the
// delivered one. A prefix of "" returns files unchanged.
func PrefixBuiltinSkillFiles(files map[string][]byte, prefix string) map[string][]byte {
	if prefix == "" {
		return files
	}
	builtin := map[string]bool{}
	for _, name := range builtinSkillBareNames {
		builtin[name] = true
	}
	out := make(map[string][]byte, len(files))
	for p, data := range files {
		top, rest, nested := strings.Cut(p, "/")
		if !nested || !builtin[top] {
			out[p] = data
			continue
		}
		delivered := BuiltinSkillName(prefix, top)
		data = prefixReferences(data, prefix)
		if rest == "SKILL.md" {
			data = bytes.Replace(data, []byte("\nname: "+top+"\n"), []byte("\nname: "+delivered+"\n"), 1)
		}
		out[delivered+"/"+rest] = data
	}
	return out
}

// prefixReferences rewrites every `**<built-in skill>**` in data to the delivered name.
func prefixReferences(data []byte, prefix string) []byte {
	for _, name := range builtinSkillBareNames {
		data = bytes.ReplaceAll(data, []byte("**"+name+"**"), []byte("**"+BuiltinSkillName(prefix, name)+"**"))
	}
	return data
}

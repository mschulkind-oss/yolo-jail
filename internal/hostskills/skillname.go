package hostskills

// skillname.go writes a namespaced skill's delivered name into its COPY of SKILL.md: pi and Copilot
// key a skill on the frontmatter `name` field rather than its directory, so one skill has one name
// at every agent only if both say `<namespace>-<skill>` (docs/design/pack-naming.md §3.4, §5). The
// pack's own file is never edited.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// skillManifest is the file whose `name` a delivered skill's copy carries.
const skillManifest = "SKILL.md"

// renamedSkill is SKILL.md's content with its frontmatter `name` set to name: the existing `name:`
// line replaced, one added at the top of the frontmatter when there is none, and a frontmatter
// block added when the file has none at all. Every other byte is kept.
func renamedSkill(data []byte, name string) []byte {
	line := "name: " + name
	text := string(data)
	nl := "\n"
	if strings.HasPrefix(text, "---\r\n") {
		nl = "\r\n"
	}
	if !strings.HasPrefix(text, "---"+nl) {
		return []byte("---" + nl + line + nl + "---" + nl + text)
	}
	lines := strings.SplitAfter(text, nl)
	for i := 1; i < len(lines); i++ {
		bare := strings.TrimRight(lines[i], "\r\n")
		if bare == "---" {
			break
		}
		if strings.HasPrefix(bare, "name:") {
			lines[i] = line + nl
			return []byte(strings.Join(lines, ""))
		}
	}
	return []byte(lines[0] + line + nl + strings.Join(lines[1:], ""))
}

// writeSkillName rewrites the copied SKILL.md under dest to carry name; "" or a skill with no
// SKILL.md leaves the copy as it is.
func writeSkillName(dest, name string) error {
	if name == "" {
		return nil
	}
	path := filepath.Join(dest, skillManifest)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, renamedSkill(data, name), fi.Mode().Perm())
}

// changedSkill is changedExcept for a skill whose copy carries a rewritten `name` (writeSkillName):
// the trees are compared with SKILL.md held aside on both sides, and the copy's SKILL.md against the
// source's RENAMED form, so an unchanged namespaced skill does not read as changed on every apply.
func changedSkill(src, dest string, exclude []string, name string) bool {
	if name == "" {
		return changedExcept(src, dest, exclude)
	}
	data, err := os.ReadFile(filepath.Join(src, skillManifest))
	if os.IsNotExist(err) {
		return changedExcept(src, dest, exclude)
	}
	if err != nil {
		return true
	}
	skip := skipSetUnder(src, exclude)
	if skip == nil {
		skip = map[string]bool{}
	}
	skip[skillManifest] = true
	a, aerr := deliveredDigest(src, skip, true)
	b, berr := deliveredDigest(dest, map[string]bool{skillManifest: true}, false)
	if aerr != nil || berr != nil || a != b {
		return true
	}
	got, err := os.ReadFile(filepath.Join(dest, skillManifest))
	return err != nil || !bytes.Equal(got, renamedSkill(data, name))
}

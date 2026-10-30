package config

import (
	"fmt"
	"strings"
)

// renamedKeyHint says what to type instead for a legacy key the generic edit verbs cannot
// address. The replacement is the same for a TOML and a legacy JSONC target: the readable-config
// schema reads its names from either format (a JSONC file's legacy `packages` is lowered to
// install.nix on read), so the hint never depends on the target file. It is empty for a key
// nothing renamed.
func renamedKeyHint(parts []string) string {
	replacement := renamedKeyReplacement(parts)
	if replacement == "" {
		return ""
	}
	return fmt.Sprintf("; %s was renamed, so use %s instead", strings.Join(parts, "."), replacement)
}

// renamedKeyReplacement names what replaced the legacy key at parts, or "". An exact descriptor
// wins; a path under a moved root (resources.cpus) carries its tail to the new root; the two
// roots with no one-line rename (loopholes, profiles) name their verb or key.
func renamedKeyReplacement(parts []string) string {
	old := strings.Join(parts, ".")
	var plain []string
	for _, d := range RenameDescriptors() {
		if d.OldPath != old {
			continue
		}
		for _, r := range d.Replacements {
			if r != "" && !strings.ContainsAny(r, " <>*\n") {
				plain = append(plain, r)
			}
		}
	}
	if len(plain) > 0 {
		return strings.Join(plain, " or ")
	}
	switch parts[0] {
	case "loopholes":
		if len(parts) >= 3 && parts[2] == "settings" {
			return fmt.Sprintf("allow.%s.<setting>", parts[1])
		}
		return "`yolo loopholes enable <name>` (this workspace, run on the host) or `yolo pack add <pack>` for a pack that ships it"
	case "profiles":
		return "providers.<name> and providers.<name>.variants.<variant>, chosen with `yolo active-provider`"
	}
	for _, d := range RenameDescriptors() {
		if d.OldPath == parts[0] && d.Transform == RenameMove && len(d.Replacements) == 1 {
			return strings.Join(append([]string{d.Replacements[0]}, parts[1:]...), ".")
		}
	}
	return ""
}

// RenamedKeyReplacement is renamedKeyReplacement for a reader outside the edit verbs (`yolo
// config why`), so advice about a legacy key names what replaced it rather than a verb that
// refuses it.
func RenamedKeyReplacement(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return renamedKeyReplacement(parts)
}

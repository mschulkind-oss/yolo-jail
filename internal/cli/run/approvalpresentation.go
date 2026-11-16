package run

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

const approvalDiffFooter = "Raw JSON comparison (read-only, records nothing): yolo config approval-diff"
const approvalReplayFallback = "The change list could not account for every difference; showing the raw comparison instead."

var approvalAreaLabels = map[string]string{
	"agents_md_extra": "Extra briefing text", "briefing_provenance": "Briefing origin labels",
	"devices": "Device access requests", "env": "Environment variables", "ephemeral_storage": "Temporary storage",
	"host_files": "Files provisioned into the environment", "include_if_found": "Optional included config files",
	"install": "Nix packages and mise tools", "kvm": "KVM device access", "lsp_servers": "Language servers",
	"machine": "GPU, resources, ports and forwarding", "mcp_servers": "MCP servers", "model_facts": "Model metadata",
	"mounts": "Paths outside the workspace", "network": "Network mode", "per_side_paths": "Per-side workspace subpaths",
	"providers": "Provider routes", "provisioners": "Program installation order", "prune": "Disk-usage warning",
	"repos": "Named repositories", "requests": "Permission requests", "required_capabilities": "Required capabilities",
	"security": "Blocked-tool shims", "workspace_readonly": "Read-only workspace", "writable_home_dirs": "Writable home directories",
	// Workspace-readable legacy spellings retain their exact paths and use the replacement key's label.
	"packages": "Nix packages and mise tools", "mise_tools": "Nix packages and mise tools",
	"gpu": "GPU, resources, ports and forwarding", "resources": "GPU, resources, ports and forwarding",
	"env_sources": "Environment variables",
}

type approvalArea struct {
	parts  []config.ApprovalPathPart
	name   string
	label  string
	first  int
	groups []config.ApprovalChangeGroup
}

// approvalPresentationLines formats both complete views from a replay-verified change set.
// width is zero for non-TTY output; terminal output uses a minimum width of 40.
func approvalPresentationLines(changes *config.ApprovalChangeSet, approved bool, width int) []string {
	if changes == nil || !changes.ReplayVerified {
		return nil
	}
	groups := changes.Groups
	if len(groups) == 0 {
		return []string{"Workspace config: no config-key paths changed."}
	}
	areas := approvalAreas(changes, groups)
	out := []string{fmt.Sprintf("Workspace config: %d change%s in %d area%s", len(groups), approvalPlural(len(groups)), len(areas), approvalPlural(len(areas))),
		"  (merged project/local files and their includes; user config is not part of this approval)", ""}
	heading := "Interpreted  (grouping and labels only; the Exact block is what you approve)"
	if approved {
		heading = "Interpreted  (grouping and labels only; the Exact block was approved)"
	}
	out = append(out, heading)
	for _, area := range areas {
		header := area.name + ": " + area.label
		if area.label == "" {
			header = area.name + "  (no description for this key)"
		}
		if area.name == "mounts" {
			header += " (resolved host paths: see the mount block above)"
		}
		out = append(out, "  "+header)
		for _, group := range area.groups {
			leaf := relativeApprovalPath(group.PathParts, area.parts)
			phrase := interpretedEffect(group)
			if leaf == "" {
				out = append(out, fmt.Sprintf("    [%d] %s", group.Number, phrase))
			} else {
				out = append(out, fmt.Sprintf("    [%d] %s: %s", group.Number, leaf, phrase))
			}
		}
	}
	out = append(out, "", "Exact")
	for _, group := range groups {
		out = append(out, exactGroupLines(group)...)
	}
	if width > 0 {
		for i, line := range out {
			out[i] = wrapApprovalLine(line, width)
		}
	}
	return out
}

func approvalAreas(changes *config.ApprovalChangeSet, groups []config.ApprovalChangeGroup) []approvalArea {
	areas := make([]approvalArea, 0)
	byName := make(map[string]int)
	for _, group := range groups {
		parts := approvalAreaParts(changes, group.PathParts)
		name := displayApprovalPath(parts)
		if name == "" {
			name = "(root)"
		}
		idx, exists := byName[name]
		if !exists {
			label := approvalAreaLabels[parts[0].Key]
			areas = append(areas, approvalArea{parts: parts, name: name, label: label, first: group.Number})
			idx = len(areas) - 1
			byName[name] = idx
		}
		areas[idx].groups = append(areas[idx].groups, group)
	}
	sort.SliceStable(areas, func(i, j int) bool { return areas[i].first < areas[j].first })
	return areas
}

func approvalAreaParts(changes *config.ApprovalChangeSet, path []config.ApprovalPathPart) []config.ApprovalPathPart {
	if len(path) > 1 && !path[1].ArrayIndex {
		prefix := path[:2]
		if isApprovalTableAt(changes.PreviousTree, prefix) || isApprovalTableAt(changes.CurrentTree, prefix) {
			return append([]config.ApprovalPathPart(nil), prefix...)
		}
	}
	if len(path) == 0 {
		return nil
	}
	return append([]config.ApprovalPathPart(nil), path[:1]...)
}

func isApprovalTableAt(root *jsonx.OrderedMap, path []config.ApprovalPathPart) bool {
	value, ok := approvalTreeValue(root, path)
	if !ok {
		return false
	}
	_, ok = value.(*jsonx.OrderedMap)
	return ok
}

func approvalTreeValue(root *jsonx.OrderedMap, path []config.ApprovalPathPart) (any, bool) {
	var value any = root
	for _, part := range path {
		if part.ArrayIndex {
			list, ok := value.([]any)
			if !ok || part.Index < 0 || part.Index >= len(list) {
				return nil, false
			}
			value = list[part.Index]
		} else {
			object, ok := value.(*jsonx.OrderedMap)
			if !ok {
				return nil, false
			}
			var exists bool
			value, exists = object.Get(part.Key)
			if !exists {
				return nil, false
			}
		}
	}
	return value, true
}

func relativeApprovalPath(path, area []config.ApprovalPathPart) string {
	if len(path) <= len(area) {
		return ""
	}
	return displayApprovalPath(path[len(area):])
}

func displayApprovalPath(parts []config.ApprovalPathPart) string {
	var b strings.Builder
	for _, part := range parts {
		if part.ArrayIndex {
			fmt.Fprintf(&b, "[%d]", part.Index+1)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		if bareApprovalKey(part.Key) {
			b.WriteString(part.Key)
		} else {
			b.WriteString(tomlBasicString(part.Key))
		}
	}
	return b.String()
}

func interpretedEffect(group config.ApprovalChangeGroup) string {
	if group.Reordered {
		list, _ := group.Previous.([]any)
		return fmt.Sprintf("reorders the same %d items; order is part of the approval", len(list))
	}
	if group.Verb == "changed" && (len(group.AddedItems)+len(group.RemovedItems) > 0) {
		var phrases []string
		if len(group.AddedItems) > 0 {
			phrases = append(phrases, listEffect("adds", group.AddedItems))
		}
		if len(group.RemovedItems) > 0 {
			phrases = append(phrases, listEffect("removes", group.RemovedItems))
		}
		return strings.Join(phrases, "; ")
	}
	if group.Verb == "added" || group.Verb == "removed" {
		value := group.Current
		if group.Verb == "removed" {
			value = group.Previous
		}
		if _, isTable := value.(*jsonx.OrderedMap); isTable {
			return fmt.Sprintf("%s this table (%d values)", verbFor(group.Verb), terminalValueCount(value))
		}
		if group.ArrayTableItem {
			listLength := group.CurrentListLength
			if group.Verb == "removed" {
				listLength = group.PreviousListLength
			}
			if _, isTable := value.(*jsonx.OrderedMap); isTable {
				return fmt.Sprintf("%s item %d of %d (table; %d values)", verbFor(group.Verb), group.ArrayIndex+1, listLength, terminalValueCount(value))
			}
			return fmt.Sprintf("%s item %d of %d: %s", verbFor(group.Verb), group.ArrayIndex+1, listLength, tomlLiteral(value))
		}
		if group.Verb == "added" {
			if list, ok := value.([]any); ok && len(list) == 0 {
				return "sets to an empty list"
			}
			if object, ok := value.(*jsonx.OrderedMap); ok && object.Len() == 0 {
				return "sets to an empty table"
			}
			return shortValuePhrase("sets to", value, group.Number)
		}
		literal := tomlLiteral(value)
		if len(literal) > 40 {
			literal = "(see [" + strconv.Itoa(group.Number) + "] below)"
		}
		return "stops setting it (was " + literal + "); user config or the built-in default applies and is not part of this approval"
	}
	if group.Verb == "changed" {
		oldTable, wasTable := group.Previous.(*jsonx.OrderedMap)
		newTable, nowTable := group.Current.(*jsonx.OrderedMap)
		if wasTable != nowTable {
			if wasTable {
				return fmt.Sprintf("replaces this table (%d values) with %s", terminalValueCount(oldTable), shortValue(tomlLiteral(group.Current), group.Number))
			}
			return fmt.Sprintf("replaces this %s with a table (%d values)", valueShape(group.Previous), terminalValueCount(newTable))
		}
		if len(tomlLiteral(group.Previous)) > 40 || len(tomlLiteral(group.Current)) > 40 {
			return "changes value (see [" + strconv.Itoa(group.Number) + "] below)"
		}
		return "changes from " + tomlLiteral(group.Previous) + " to " + tomlLiteral(group.Current)
	}
	return "changes value (see [" + strconv.Itoa(group.Number) + "] below)"
}

func shortValue(value string, number int) string {
	if len(value) > 40 {
		return "(see [" + strconv.Itoa(number) + "] below)"
	}
	return value
}

func shortValuePhrase(phrase string, value any, number int) string {
	return phrase + " " + shortValue(tomlLiteral(value), number)
}

func listEffect(verb string, items []any) string {
	prefix := fmt.Sprintf("%s %d item%s", verb, len(items), approvalPlural(len(items)))
	if len(items) > 5 {
		return prefix + "; see the Exact entry below"
	}
	var literals []string
	for _, item := range items {
		literal := tomlLiteral(item)
		if len(literal) > 40 {
			return prefix + "; see the Exact entry below"
		}
		literals = append(literals, literal)
	}
	return prefix + ": " + strings.Join(literals, ", ")
}

func exactGroupLines(group config.ApprovalChangeGroup) []string {
	path := displayApprovalPath(group.PathParts)
	if path == "" {
		path = "(root)"
	}
	if group.Reordered {
		return []string{fmt.Sprintf("  [%d] reordered %s (same %d items; all positions shown)", group.Number, path, len(group.Previous.([]any))),
			"        was  " + tomlLiteral(group.Previous), "        now  " + tomlLiteral(group.Current)}
	}
	if group.ArrayTableItem && (group.Added || group.Removed) {
		value := group.Current
		if group.Verb == "removed" {
			value = group.Previous
		}
		if _, isTable := value.(*jsonx.OrderedMap); isTable {
			listLength := group.CurrentListLength
			if group.Verb == "removed" {
				listLength = group.PreviousListLength
			}
			side := "current"
			if group.Verb == "removed" {
				side = "previous"
			}
			lines := []string{fmt.Sprintf("  [%d] %s %s (%s item %d of %d; table; %d terminal values)", group.Number, group.Verb, path, side, group.ArrayIndex+1, listLength, terminalValueCount(value))}
			return append(lines, tableValueLines(path, value)...)
		}
	}
	if group.Verb == "added" || group.Verb == "removed" {
		value := group.Current
		if group.Verb == "removed" {
			value = group.Previous
		}
		if _, table := value.(*jsonx.OrderedMap); table {
			lines := []string{fmt.Sprintf("  [%d] %s %s (table; %d terminal values)", group.Number, group.Verb, path, terminalValueCount(value))}
			return append(lines, tableValueLines(path, value)...)
		}
		literal := tomlLiteral(value)
		if group.Verb == "removed" {
			return []string{fmt.Sprintf("  [%d] %-8s %s", group.Number, group.Verb, path), "        was  " + literal}
		}
		return []string{fmt.Sprintf("  [%d] %-8s %s", group.Number, group.Verb, path), "        " + literal}
	}
	if len(group.AddedItems)+len(group.RemovedItems) > 0 {
		oldList, _ := group.Previous.([]any)
		curList, _ := group.Current.([]any)
		lines := []string{fmt.Sprintf("  [%d] changed   %s   (%d item%s added, %d removed)", group.Number, path, len(group.AddedItems), approvalPlural(len(group.AddedItems)), len(group.RemovedItems))}
		for i, value := range group.RemovedItems {
			lines = append(lines, fmt.Sprintf("        removed  %s   (previous item %d of %d)", tomlLiteral(value), group.RemovedPositions[i], len(oldList)))
		}
		for i, value := range group.AddedItems {
			lines = append(lines, fmt.Sprintf("        added    %s   (current item %d of %d)", tomlLiteral(value), group.AddedPositions[i], len(curList)))
		}
		return lines
	}
	oldTable, wasTable := group.Previous.(*jsonx.OrderedMap)
	newTable, nowTable := group.Current.(*jsonx.OrderedMap)
	if wasTable || nowTable {
		heading := "table replaced by a value"
		if nowTable {
			heading = valueShape(group.Previous) + " replaced by a table"
		}
		lines := []string{fmt.Sprintf("  [%d] changed   %s (%s)", group.Number, path, heading)}
		if wasTable {
			lines = append(lines, append([]string{"        was"}, tableValueLines(path, oldTable)...)...)
		} else {
			lines = append(lines, "        was  "+tomlLiteral(group.Previous))
		}
		if nowTable {
			lines = append(lines, append([]string{"        now"}, tableValueLines(path, newTable)...)...)
		} else {
			lines = append(lines, "        now  "+tomlLiteral(group.Current))
		}
		return lines
	}
	return []string{fmt.Sprintf("  [%d] changed   %s", group.Number, path), "        was  " + tomlLiteral(group.Previous), "        now  " + tomlLiteral(group.Current)}
}

func tableValueLines(path string, value any) []string {
	object, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return []string{"        " + path + " = " + tomlLiteral(value)}
	}
	if object.Len() == 0 {
		return []string{"        " + path + " = {}"}
	}
	var lines []string
	keys := append([]string(nil), object.Keys()...)
	sort.Strings(keys)
	for _, key := range keys {
		child, _ := object.Get(key)
		childPath := path + "." + displayApprovalPath([]config.ApprovalPathPart{{Key: key}})
		if nested, ok := child.(*jsonx.OrderedMap); ok && nested.Len() > 0 {
			lines = append(lines, tableValueLines(childPath, nested)...)
			continue
		}
		lines = append(lines, "        "+childPath+" = "+tomlLiteral(child))
	}
	return lines
}

func terminalValueCount(value any) int {
	object, ok := value.(*jsonx.OrderedMap)
	if !ok || object.Len() == 0 {
		return 1
	}
	total := 0
	for _, key := range object.Keys() {
		child, _ := object.Get(key)
		total += terminalValueCount(child)
	}
	return total
}

func valueShape(value any) string {
	switch value.(type) {
	case *jsonx.OrderedMap:
		return "table"
	case []any:
		return "list"
	default:
		return "value"
	}
}

func verbFor(verb string) string {
	if verb == "added" {
		return "adds"
	}
	return "removes"
}
func approvalPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func tomlLiteral(value any) string {
	switch v := value.(type) {
	case nil:
		return "(null)"
	case string:
		return tomlBasicString(v)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = tomlLiteral(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *jsonx.OrderedMap:
		keys := append([]string(nil), v.Keys()...)
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			item, _ := v.Get(key)
			parts = append(parts, displayApprovalPath([]config.ApprovalPathPart{{Key: key}})+" = "+tomlLiteral(item))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		if text, err := jsonx.DumpsCompact(v); err == nil {
			return text
		}
		return fmt.Sprint(v)
	}
}

func bareApprovalKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func tomlBasicString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case '[':
			b.WriteString(`\u005B`)
		case ']':
			b.WriteString(`\u005D`)
		default:
			if r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0xffff {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else if r > 0xffff {
				fmt.Fprintf(&b, `\U%08X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func wrapApprovalLine(line string, width int) string {
	if width < 40 {
		width = 40
	}
	var out strings.Builder
	col, indent := 0, 0
	for i := 0; i < len(line); {
		if col == 0 && out.Len() == 0 {
			for i < len(line) && line[i] == ' ' {
				out.WriteByte(' ')
				i++
				col++
			}
			indent = col
		}
		if col >= width {
			out.WriteByte('\n')
			for j := 0; j < indent+2; j++ {
				out.WriteByte(' ')
			}
			col = indent + 2
		}
		n := 1
		if line[i] == '\\' && i+1 < len(line) {
			switch line[i+1] {
			case 'u':
				n = min(6, len(line)-i)
			case 'U':
				n = min(10, len(line)-i)
			default:
				n = 2
			}
		}
		if col+n > width && col > indent {
			out.WriteByte('\n')
			for j := 0; j < indent+2; j++ {
				out.WriteByte(' ')
			}
			col = indent + 2
		}
		out.WriteString(line[i : i+n])
		i += n
		col += n
	}
	return out.String()
}

func approvalPresentationWidth(o *Options) int {
	if o.IsTTYStderr == nil || !o.IsTTYStderr() {
		return 0
	}
	width := tty.Width(os.Stderr.Fd())
	if width < 40 {
		return 40
	}
	return width
}

func printApprovalPresentation(out printer, changes *config.ApprovalChangeSet, configChanged, accepted bool, diffLines []string, width int) {
	if !configChanged {
		out.print("Workspace config: no change.")
		return
	}
	if changes == nil || !changes.ReplayVerified {
		out.print(approvalReplayFallback)
		printConfigDiff(out, diffLines)
	} else {
		for _, line := range approvalPresentationLines(changes, accepted, width) {
			switch {
			case strings.HasPrefix(line, "Interpreted"), strings.HasPrefix(line, "Exact"):
				out.printf("[bold]%s[/bold]", richtext.Escape(line))
			case strings.Contains(line, "] added "), strings.Contains(line, "] adds "), strings.Contains(line, "        added "):
				out.printf("[green]%s[/green]", richtext.Escape(line))
			case strings.Contains(line, "] removed "), strings.Contains(line, "] removes "), strings.Contains(line, "        removed "):
				out.printf("[red]%s[/red]", richtext.Escape(line))
			default:
				out.print(richtext.Escape(line))
			}
		}
	}
}

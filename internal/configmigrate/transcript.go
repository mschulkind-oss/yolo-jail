package configmigrate

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// Transcript is what `yolo config migrate` shows before it asks (config-migration.md §1): the old
// files, what is not on this machine, each file's summary, the equality verdict and the records.
func (p *MigrationPlan) Transcript() string {
	var b strings.Builder
	old, rewritten := p.Graph.Old(), p.Graph.Rewritten()
	if len(old) == 0 && len(rewritten) == 0 {
		if p.Equal {
			b.WriteString("No config file here is in the old format; nothing to migrate.\n")
			return b.String()
		}
		// A TOML file holding a key named setups replace that this migration cannot convert
		// (SUP-D17): the refusal names each one's own next step.
		b.WriteString("No config file here is in the old format, but ")
		b.WriteString((&InequalityError{Plan: p}).Error())
		b.WriteString("\n")
		return b.String()
	}
	// Each heading is followed by its own files: the old-format files, then the TOML files
	// rewritten for a removed setup key.
	if len(old) > 0 {
		fmt.Fprintf(&b, "Found %d %s in the old format:\n", len(old), plural(len(old), "file", "files"))
	}
	for _, f := range old {
		via := ""
		if f.Via != "" {
			via = ", via include_if_found"
		}
		fmt.Fprintf(&b, "  %s  (%d lines%s)\n", f.Logical, countLines(f.Bytes), via)
	}
	if len(rewritten) > 0 {
		fmt.Fprintf(&b, "Found %d TOML %s holding keys named setups replace:\n", len(rewritten), plural(len(rewritten), "file", "files"))
		for _, f := range rewritten {
			fmt.Fprintf(&b, "  %s\n", f.Logical)
		}
	}
	for _, m := range p.Unreachable {
		fmt.Fprintf(&b, "Not found here (listed in %s): %s.\n  Run `yolo config migrate` on a machine that has it.\n", m.ListedIn, m.Path)
	}
	for _, t := range p.Targets {
		fmt.Fprintf(&b, "\n--- %s\n+++ %s\n", t.Old, t.New)
		if t.New == t.Old {
			if len(t.Respelled) > 0 {
				fmt.Fprintf(&b, "  (include %s respelled to its .toml twin)\n", strings.Join(t.Respelled, ", "))
			}
			for _, line := range t.Rewrote {
				fmt.Fprintf(&b, "  %s\n", line)
			}
			for _, line := range t.Ceiling {
				fmt.Fprintf(&b, "  + %s: the converted workspace requests it, and its old switch admitted it\n", line)
			}
			b.WriteString("  (nothing else changes)\n")
			continue
		}
		fmt.Fprintf(&b, "  (%d lines → %d; %d %s kept, in order)\n", countLines(t.File.Bytes), countLines(t.Bytes), t.Comments, plural(t.Comments, "comment", "comments"))
		for _, d := range t.Dropped {
			fmt.Fprintf(&b, "  %q: %s dropped: it is the default\n", d.Path, d.Value)
		}
		for _, d := range t.Inert {
			fmt.Fprintf(&b, "  %q: %s dropped: %s\n", d.Path, d.Value, d.Why)
		}
		for _, f := range t.Folded {
			fmt.Fprintf(&b, "  %q: written on each of %d models, folded into one\n", f.Path, f.Rows)
		}
		for _, tw := range t.Twins {
			fmt.Fprintf(&b, "  %q: written as twin_of %q, the row whose id it sends\n", tw.Path, tw.Of)
		}
	}
	b.WriteString("\n")
	// A linked file is never written through: said once, standing alone, before anything else
	// the plan concludes (RC-D41).
	if w := LinkWarning(p.Graph.Linked()); w != "" {
		b.WriteString(w + "\n\n")
	}
	if !p.Equal {
		b.WriteString((&InequalityError{Plan: p}).Error())
		b.WriteString("\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Effective config before and after: identical (checked as canonical JSON, %d bytes).\n", p.CanonicalBytes)
	if p.Refused == nil {
		b.WriteString("The next launch's config checks pass on the converted files (run on a staged copy; nothing was changed yet).\n")
	}
	for _, u := range p.Unchecked {
		fmt.Fprintf(&b, "  Not checked: %s.\n", u)
	}
	if len(p.Renames) > 0 {
		b.WriteString(RenamesHeading + "\n")
		for _, row := range RenameRows(p.Renames) {
			fmt.Fprintf(&b, "  %s%s  →  %s\n", row[0], row[1], row[2])
		}
		b.WriteString(RenamesClosing(p.Renames) + "\n")
	}
	for _, d := range p.DerivedVendors {
		fmt.Fprintf(&b, "  vendor %q follows from the id of %q, compared as derived; the launch now carries it.\n",
			d.Vendor, dotted([]string{"providers", d.Provider, "models", d.Model}))
	}
	if s := Respelled(p.Records); s != "" {
		fmt.Fprintf(&b, "Records only yolo reads, respelled in place: %s.\n", s)
	}
	for _, r := range p.Records {
		if r.State == RecordLeft {
			fmt.Fprintf(&b, "  %s %s is left: %s.\n", r.Kind, r.Path, r.Why)
		}
	}
	for _, ig := range p.Ignores {
		b.WriteString(ig.Plan(p.Graph.Request.Workspace) + ".\n")
	}
	for _, line := range UnaliasedLines(p) {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// UnaliasedLines are the closing report's lines for each provider the conversion gave variants
// and no alias (RC-D62, docs/design/readable-config.md#RC-D62): a variant is selected as
// provider/variant and nothing shorter, so the line names the one command that gives a short
// name. One line per provider, however many files wrote its variants.
func UnaliasedLines(p *MigrationPlan) []string {
	var order []string
	variants := map[string][]string{}
	for _, t := range p.Targets {
		for _, u := range t.Unaliased {
			if _, seen := variants[u.Provider]; !seen {
				order = append(order, u.Provider)
			}
			variants[u.Provider] = append(variants[u.Provider], u.Variants...)
		}
	}
	lines := make([]string, 0, len(order))
	for _, provider := range order {
		names := make([]string, len(variants[provider]))
		for i, v := range variants[provider] {
			names[i] = provider + "/" + v
		}
		lines = append(lines, fmt.Sprintf("%s: its %s selected as %s; for one short name, `yolo config set providers.%s.alias %s` (an alias starts on the provider's own `model`).",
			provider, plural(len(names), "variant is", "variants are"), joinWithAnd(names), provider, shquote.Quote(variants[provider][0])))
	}
	return lines
}

// joinWithAnd is "a", "a and b", "a, b and c".
func joinWithAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// Respelled counts the records respelled in place by kind, in the order each kind first
// appears ("3 approval records, 2 boot baselines"); "" when there are none.
func Respelled(records []Record) string {
	var kinds []string
	count := map[string]int{}
	for _, r := range records {
		if r.State != RecordRespell {
			continue
		}
		if count[r.Kind] == 0 {
			kinds = append(kinds, r.Kind)
		}
		count[r.Kind]++
	}
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = fmt.Sprintf("%d %s", count[k], plural(count[k], k, k+"s"))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

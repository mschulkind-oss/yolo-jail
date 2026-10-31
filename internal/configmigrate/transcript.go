package configmigrate

import (
	"fmt"
	"strings"
)

// Transcript is what `yolo config migrate` shows before it asks (config-migration.md §1): the old
// files, what is not on this machine, each file's summary, the equality verdict and the records.
func (p *MigrationPlan) Transcript() string {
	var b strings.Builder
	old := p.Graph.Old()
	if len(old) == 0 {
		b.WriteString("No config file here is in the old format; nothing to migrate.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Found %d %s in the old format:\n", len(old), plural(len(old), "file", "files"))
	for _, f := range old {
		via := ""
		if f.Via != "" {
			via = ", via include_if_found"
		}
		fmt.Fprintf(&b, "  %s  (%d lines%s)\n", f.Logical, countLines(f.Bytes), via)
	}
	for _, m := range p.Unreachable {
		fmt.Fprintf(&b, "Not found here (listed in %s): %s.\n  Run `yolo config migrate` on a machine that has it.\n", m.ListedIn, m.Path)
	}
	for _, t := range p.Targets {
		fmt.Fprintf(&b, "\n--- %s\n+++ %s\n", t.Old, t.New)
		if t.New == t.Old {
			fmt.Fprintf(&b, "  (include %s respelled to its .toml twin; nothing else changes)\n", strings.Join(t.Respelled, ", "))
			continue
		}
		fmt.Fprintf(&b, "  (%d lines → %d; %d %s kept, in order)\n", countLines(t.File.Bytes), countLines(t.Bytes), t.Comments, plural(t.Comments, "comment", "comments"))
		for _, d := range t.Dropped {
			fmt.Fprintf(&b, "  %q: %s dropped: it is the default\n", d.Path, d.Value)
		}
		for _, f := range t.Folded {
			fmt.Fprintf(&b, "  %q: written on each of %d models, folded into one\n", f.Path, f.Rows)
		}
		for _, tw := range t.Twins {
			fmt.Fprintf(&b, "  %q: written as twin_of %q, the row whose id it sends\n", tw.Path, tw.Of)
		}
		if t.Link != nil {
			fmt.Fprintf(&b, "  the symlink %s is renamed to %s\n", t.Link.Old, t.Link.Path)
		}
	}
	b.WriteString("\n")
	if !p.Equal {
		b.WriteString((&InequalityError{Plan: p}).Error())
		b.WriteString("\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Effective config before and after: identical (checked as canonical JSON, %d bytes).\n", p.CanonicalBytes)
	if p.Refused == nil {
		b.WriteString("The next launch's config checks pass on the converted files (run on a staged copy; nothing was changed yet).\n")
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
	return b.String()
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

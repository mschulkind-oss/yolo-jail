package hostskills

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// PrintSourcedSkillProjection reports supporting deployments separately from
// logical registrations. It reads only the invocation's already validated rows.
func PrintSourcedSkillProjection(pr richtext.Printer, packs []*packload.Pack) {
	printSkillProjection(pr, packs, false)
}

// PrintSourcedSkillInspection reports read-only plans, not a materialized deployment.
func PrintSourcedSkillInspection(pr richtext.Printer, packs []*packload.Pack) {
	printSkillProjection(pr, packs, true)
}

func printSkillProjection(pr richtext.Printer, packs []*packload.Pack, inspection bool) {
	seen := map[string]bool{}
	for _, p := range packs {
		if p.Projection != nil {
			s := p.Projection
			pr.Printf("  scoped pack %s: %s, subdir=%q dir=%q; source %s @ %s", richtext.Escape(p.Name), s.Mode, s.Subdir, s.Selection.Dir, richtext.Escape(s.Source), s.Commit)
			pr.Printf("    candidates: %v; selected: %v; excluded: %v", s.Candidates, s.Selected, s.Excluded)
			pr.Printf("    origin: %s; projection %s", richtext.Escape(s.Origin), s.Digest)
			if inspection {
				pr.Printf("    conventional-skill-folders=%d (markerless directories excluded)", len(packload.SkillNames(p)))
			}
			kinds := make([]string, 0, len(s.Omitted))
			for kind := range s.Omitted {
				kinds = append(kinds, kind)
			}
			sort.Strings(kinds)
			for _, kind := range kinds {
				pr.Printf("    intentionally omitted %s: %d", richtext.Escape(kind), s.Omitted[kind])
			}
		}
		for _, row := range p.SourcedSkillRows() {
			if inspection && p.Projection != nil {
				s := p.Projection.Rows[row.Row]
				name := packname.Delivered(PackLayer(p).Namespace, row.By.SkillBaseName())
				carrier := "Git immutable payload"
				if row.LocalCarrier {
					carrier = "selected-directory carrier (not published)"
				}
				source, directory, commit := p.Projection.Source, s.SourceDirectory, s.ResolvedCommit
				if directory == "" {
					directory = s.Path
				}
				if commit == "" {
					commit = p.Projection.Commit
				}
				if s.Sourced {
					address, _ := packsrc.Parse(row.By.Source)
					source, directory = address.Repo, address.Path
				}
				pr.Printf("    row %s → %s: kind=%s marker=%s; carrier=%s; source root=%s directory=%q commit=%s", richtext.Escape(s.Name), richtext.Escape(name), s.Kind, s.MarkerState, carrier, richtext.Escape(source), directory, commit)
				if s.Kind == "directory" {
					pr.Printf("      ordinary-directory governor: source=%q child=%q", s.Governor, s.Name)
				}
				if s.MarkerState == "absent" {
					pr.Printf("      folder, no SKILL.md, delivered but not a discoverable skill; excluded from skill counts")
				}
				pr.Printf("      origin: %s", richtext.Escape(p.Projection.Origin))
				if row.Outcome.State == packsrc.SkillNotFetched || row.Outcome.State == packsrc.SkillNotMaterialized {
					pr.Printf("      payload: %s — %s", row.Outcome.State, richtext.Escape(row.Outcome.Diagnostic(fmt.Sprintf("pack %s skills[%d]", p.Name, row.Row))))
				}
				if row.Plan != nil {
					pr.Printf("      license: %s", richtext.Escape(row.Plan.LicenseNote))
				} else {
					pr.Printf("      license: determined at publication")
				}
				if row.Outcome.Plan != nil {
					for _, omission := range row.Outcome.Plan.Omissions {
						pr.Printf("      supporting data omitted %q: %s", omission.Path, richtext.Escape(omission.Reason))
					}
				} else {
					pr.Printf("      supporting-data omissions: determined at publication")
				}
				continue
			}
			label := fmt.Sprintf("pack %s skills[%d]", p.Name, row.Row)
			name := packname.Delivered(PackLayer(p).Namespace, row.By.SkillBaseName())
			if row.Withheld != "" {
				pr.Printf("  registration %s: WITHHELD — %s", richtext.Escape(name), richtext.Escape(row.Withheld))
				continue
			}
			result := row.Outcome
			pair := result.Repo + "\n" + result.Ref
			if result.State != packsrc.SkillReady || row.Plan == nil {
				pr.Printf("  registration %s: %s", richtext.Escape(name), richtext.Escape(result.Diagnostic(label)))
				if !seen[pair] {
					seen[pair] = true
					pr.Printf("  payload %s?ref=%s: %s", richtext.Escape(result.Repo), richtext.Escape(result.Ref), strings.ToUpper(string(result.State)))
				}
				continue
			}
			id := packsrc.SkillPayloadIdentity(result.Repo, result.Commit)
			if row.LocalCarrier {
				id = row.Outcome.Payload
				if !seen[id] {
					seen[id] = true
					var size int64
					for _, entry := range result.Plan.Entries {
						size += entry.Size
					}
					pr.Printf("  copied selected carrier: %d files, %d bytes; no original pack discovery", result.Plan.Files, size)
				}
			} else if !seen[id] {
				seen[id] = true
				var size int64
				for _, entry := range result.Plan.Entries {
					size += entry.Size
				}
				pr.Printf("  payload deployment %s?ref=%s @ %s: %d files, %d bytes, %d executables, %d omissions", richtext.Escape(result.Repo), richtext.Escape(result.Ref), result.Commit, result.Plan.Files, size, result.Plan.Executables, len(result.Plan.Omissions))
				pr.Printf("    whole repository readable as supporting data; unselected metadata is not activated")
				for _, omission := range result.Plan.Omissions {
					pr.Printf("    omitted %q — %s", omission.Path, richtext.Escape(omission.Reason))
				}
			} else {
				pr.Printf("  payload %s?ref=%s @ %s: shared supporting deployment (no added bytes)", richtext.Escape(result.Repo), richtext.Escape(result.Ref), result.Commit)
			}
			audience := strings.Join(row.By.Agents, ",")
			if audience == "" {
				audience = "all selected destinations"
			}
			if row.Plan.MarkerState == "absent" {
				pr.Printf("  delivers folder %s to %s: no SKILL.md, not discoverable and excluded from skill counts; %d selected executables; supporting links into %s", richtext.Escape(name), richtext.Escape(audience), row.Plan.Executables, richtext.Escape(filepath.ToSlash(row.Plan.Path)))
				pr.Printf("    license: %s", richtext.Escape(row.Plan.LicenseNote))
			} else {
				pr.Printf("  delivers registration %s to %s: private regular SKILL.md; %d selected executables; supporting links into %s", richtext.Escape(name), richtext.Escape(audience), row.Plan.Executables, richtext.Escape(filepath.ToSlash(row.Plan.Path)))
				pr.Printf("    marker name rewritten to %s; license: %s", richtext.Escape(name), richtext.Escape(row.Plan.LicenseNote))
			}
			if result.Warning != "" {
				pr.Printf("    warning: %s", richtext.Escape(result.Warning))
			}
		}
	}
}

package hostskills

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// PrintSourcedSkillProjection reports supporting deployments separately from
// logical registrations. It reads only the invocation's already validated rows.
func PrintSourcedSkillProjection(pr richtext.Printer, packs []*packload.Pack) {
	seen := map[string]bool{}
	for _, p := range packs {
		for _, row := range p.SourcedSkillRows() {
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
			if !seen[id] {
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
			pr.Printf("  delivers registration %s to %s: private regular SKILL.md; %d selected executables; supporting links into %s", richtext.Escape(name), richtext.Escape(audience), row.Plan.Executables, richtext.Escape(filepath.ToSlash(row.Plan.Path)))
			pr.Printf("    marker name rewritten to %s; license: %s", richtext.Escape(name), richtext.Escape(row.Plan.LicenseNote))
			if result.Warning != "" {
				pr.Printf("    warning: %s", richtext.Escape(result.Warning))
			}
		}
	}
}

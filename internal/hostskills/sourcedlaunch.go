package hostskills

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// launchNamesShown is how many skill names the launch line spells out before
// it says "+N more"; the full list is in `yolo check`.
const launchNamesShown = 3

// PrintSourcedSkillLaunch is the launch's disclosure of Git skill imports: one
// line per import naming the repository, the short commit, how many skills
// are selected of how many the repository offers, their names, and that the
// whole repository is readable as data. Executables, omissions, warnings and
// every unready or withheld registration each keep a line of their own, so
// compressing the progress never hides a disclosure (OQ-RO3). The candidates,
// excluded names, origin, projection digest and byte counts stay in
// PrintSourcedSkillInspection and the other full reports (`yolo check`).
func PrintSourcedSkillLaunch(pr richtext.Printer, packs []*packload.Pack) {
	seen := map[string]bool{}
	for _, p := range packs {
		rows := p.SourcedSkillRows()
		if len(rows) == 0 && p.Projection == nil {
			continue
		}
		var names []string
		var problems, details []string
		var commit string
		readable, carrier := false, false
		executables := 0
		for _, row := range rows {
			name := packname.Delivered(PackLayer(p).Namespace, row.By.SkillBaseName())
			label := fmt.Sprintf("pack %s skills[%d]", p.Name, row.Row)
			if row.Withheld != "" {
				problems = append(problems, fmt.Sprintf("  registration %s: WITHHELD — %s", richtext.Escape(name), richtext.Escape(row.Withheld)))
				continue
			}
			result := row.Outcome
			if result.State != packsrc.SkillReady || row.Plan == nil {
				problems = append(problems, fmt.Sprintf("  registration %s: %s", richtext.Escape(name), richtext.Escape(result.Diagnostic(label))))
				pair := result.Repo + "\n" + result.Ref
				if !seen[pair] {
					seen[pair] = true
					problems = append(problems, fmt.Sprintf("  payload %s?ref=%s: %s", richtext.Escape(result.Repo), richtext.Escape(result.Ref), strings.ToUpper(string(result.State))))
				}
				continue
			}
			names = append(names, name)
			if row.LocalCarrier {
				carrier = true
			} else {
				readable = true
				if commit == "" {
					commit = result.Commit
				}
			}
			id := packsrc.SkillPayloadIdentity(result.Repo, result.Commit)
			if row.LocalCarrier {
				id = result.Payload
			}
			if !seen[id] {
				seen[id] = true
				executables += result.Plan.Executables
				for _, omission := range result.Plan.Omissions {
					details = append(details, fmt.Sprintf("  skills from %s: omitted %q — %s", richtext.Escape(p.Name), omission.Path, richtext.Escape(omission.Reason)))
				}
			}
			if result.Warning != "" {
				details = append(details, fmt.Sprintf("  skills from %s: warning: %s", richtext.Escape(p.Name), richtext.Escape(result.Warning)))
			}
		}
		ready := len(names)
		total := ready
		if s := p.Projection; s != nil {
			names = append([]string(nil), s.Selected...)
			total = len(s.Candidates)
			if len(names) > total {
				total = len(names)
			}
			if commit == "" {
				commit = s.Commit
			}
			kinds := make([]string, 0, len(s.Omitted))
			for kind := range s.Omitted {
				kinds = append(kinds, kind)
			}
			sort.Strings(kinds)
			for _, kind := range kinds {
				details = append(details, fmt.Sprintf("  skills from %s: intentionally omitted %s: %d", richtext.Escape(p.Name), richtext.Escape(kind), s.Omitted[kind]))
			}
		}
		if ready == 0 && len(problems) > 0 {
			// Nothing was delivered: the problem lines are the disclosure.
			for _, line := range problems {
				pr.Printf("%s", line)
			}
			continue
		}
		at := ""
		if commit != "" {
			at = " @ " + shortCommit(commit)
		}
		tail := "repository readable as data"
		switch {
		case !readable && carrier:
			tail = "selected folders copied, not the whole repository"
		case !readable:
			tail = "not delivered"
		}
		if executables > 0 {
			tail += fmt.Sprintf("; %d executable files in it", executables)
		}
		pr.Printf("  skills from %s%s: %d of %d (%s); %s", richtext.Escape(p.Name), at, len(names), total, launchNames(names), tail)
		for _, line := range problems {
			pr.Printf("%s", line)
		}
		for _, line := range details {
			pr.Printf("%s", line)
		}
	}
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func launchNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	shown := names
	if len(shown) > launchNamesShown {
		shown = shown[:launchNamesShown]
	}
	for i, n := range shown {
		shown[i] = richtext.Escape(n)
	}
	out := strings.Join(shown, ", ")
	if more := len(names) - len(shown); more > 0 {
		out += fmt.Sprintf(", +%d more", more)
	}
	return out
}

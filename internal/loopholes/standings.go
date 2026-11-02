package loopholes

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// standings.go is what `yolo loopholes list` and `status` show of a grant-declaring loophole
// (docs/design/loophole-access-approval.md LG-D8): each declared kind's STANDING for the
// workspace, the word §4.6 defines (granted, pending, requestable, not_allowed). It reads the
// gate's own resolver (config.WithLoopholeGrants over the user's ceiling, the project's request
// and the approval record), never a second reading of either.

// KindStanding is one declared kind's standing in a workspace.
type KindStanding struct {
	Kind     string   `json:"kind"`
	Standing string   `json:"standing"`
	Values   []string `json:"values,omitempty"`
}

// ResolveStandings is each grant-declaring pack loophole in lps's standing per kind, in
// declaration order, by loophole name, for workspace. On the host the approval record decides
// granted against pending. In a jail the record is the host's and never crosses, so a kind the
// gate would hold reads `candidate` (config.LoopholeKindCandidate): granted once the human's y
// is recorded. nil, nil when none of lps declares a grant.
func ResolveStandings(workspace string, inJail bool, lps []*Loophole) (map[string][]KindStanding, error) {
	var gate []config.GateLoophole
	for _, lp := range lps {
		if lp == nil || lp.Source != SourcePack || len(lp.Grants) == 0 {
			continue
		}
		gate = append(gate, config.GateLoophole{Pack: lp.Pack, Loophole: lp.Name, Access: lp.Access,
			Grants: append([]Grant(nil), lp.Grants...)})
	}
	if len(gate) == 0 {
		return nil, nil
	}
	read, err := config.ReadWorkspaceForGate(workspace)
	if err != nil {
		return nil, err
	}
	check := config.WithLoopholeGrants(nil, workspace, read, gate)
	if check == nil {
		return nil, nil
	}
	if check.Fatal != nil {
		return nil, check.Fatal
	}
	var recorded map[string]map[string]config.LoopholeGrantStanding
	if !inJail {
		recorded = check.LoopholeGrantStandings(workspace)
	}
	out := map[string][]KindStanding{}
	for _, src := range check.LoopholeGrants {
		for _, k := range src.Kinds {
			ks := KindStanding{Kind: k.Grant.Kind, Standing: string(k.Standing)}
			if st, ok := recorded[src.Pack][k.Grant.Kind]; ok {
				ks.Standing, ks.Values = st.Standing, st.Values
			} else if len(k.Values) > 0 {
				ks.Values = append([]string(nil), k.Values...)
			}
			out[src.Loophole] = append(out[src.Loophole], ks)
		}
	}
	return out, nil
}

// standingStyle is a standing's color, by the CLI's convention (listStateStyle): green for a
// grant held, yellow for one waiting on the human's y, dim for the rest.
func standingStyle(standing string) string {
	switch standing {
	case config.LoopholeGrantGranted:
		return "green"
	case config.LoopholeGrantPending, string(config.LoopholeKindCandidate):
		return "yellow"
	default:
		return "dim"
	}
}

// standingsFor is deps.Standings over all, nil when there is no seam; a resolver error is
// written to Err once, naming `yolo check`, and nothing is shown.
func standingsFor(deps Deps, all []*Loophole) map[string][]KindStanding {
	if deps.Standings == nil {
		return nil
	}
	out, err := deps.Standings(deps.Cwd, all)
	if err != nil {
		fmt.Fprintf(deps.Err, "yolo loopholes: this project's grants could not be read, so none is shown: %v\n"+
			"  `yolo check` names where.\n", err)
		return nil
	}
	return out
}

// printStandings is the one line `list` and `status` print under a grant-declaring loophole:
// each declared kind and its standing in this workspace, a valued kind's values after it
// ("grants here: read granted, read_all not_allowed"). Nothing for an ungated loophole.
func printStandings(p richtext.Printer, kinds []KindStanding) {
	if len(kinds) == 0 {
		return
	}
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		style := standingStyle(k.Standing)
		part := richtext.Escape(k.Kind) + " [" + style + "]" + richtext.Escape(k.Standing) + "[/" + style + "]"
		if len(k.Values) > 0 {
			part += " (" + richtext.Escape(strings.Join(k.Values, ", ")) + ")"
		}
		parts = append(parts, part)
	}
	p.Printf("      grants here: %s", strings.Join(parts, ", "))
}

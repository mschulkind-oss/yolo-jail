package packdecl

import (
	"fmt"
	"sort"
)

// ModelListCheck is a program-owned, check-only description of which declared model makers a
// program can call on a provider platform. It is not part of any install or launch payload.
type ModelListCheck struct {
	Rules []ModelListCheckRule `json:"rules,omitempty"`
}

// ModelListCheckRule selects a provider platform and optionally whether a live via route reaches
// the program. An omitted Via covers both paths; an omitted Makers accepts every maker.
type ModelListCheckRule struct {
	Platform      string   `json:"platform"`
	Via           *bool    `json:"via,omitempty"`
	Makers        []string `json:"makers,omitempty"`
	NativeEmptyOK bool     `json:"native_empty_ok,omitempty"`
}

// modelListCheckProblems validates the optional check-only program fact. Selectors must be
// disjoint so manifest declaration order cannot change the check's answer.
func modelListCheckProblems(label string, check *ModelListCheck) []string {
	var problems []string
	for i, rule := range check.Rules {
		ruleLabel := fmt.Sprintf("%s.rules[%d]", label, i)
		if rule.Platform == "" {
			problems = append(problems, ruleLabel+": needs a non-empty \"platform\"")
		}
		if rule.NativeEmptyOK && (rule.Via == nil || *rule.Via) {
			problems = append(problems, ruleLabel+": \"native_empty_ok\" requires \"via\": false")
		}
		if rule.Makers != nil && len(rule.Makers) == 0 {
			problems = append(problems, ruleLabel+": omit \"makers\" for no restriction, or supply at least one maker")
		}
		seen := map[string]bool{}
		for j, maker := range rule.Makers {
			if !ValidModelVendor(maker) {
				problems = append(problems, fmt.Sprintf(
					"%s.makers[%d] %q is not one lowercase token naming a model maker", ruleLabel, j, maker))
			}
			if seen[maker] {
				problems = append(problems, fmt.Sprintf("%s.makers lists %q twice", ruleLabel, maker))
			}
			seen[maker] = true
		}
		for j := 0; j < i; j++ {
			prior := check.Rules[j]
			if prior.Platform != rule.Platform {
				continue
			}
			if prior.Via == nil || rule.Via == nil || *prior.Via == *rule.Via {
				problems = append(problems, fmt.Sprintf(
					"%s overlaps rules[%d] on platform %q and via path; selectors must not overlap",
					ruleLabel, j, rule.Platform))
			}
		}
	}
	return problems
}

// validateModelListChecks binds the pack-wide fact to local program ownership only. A fork
// supplies bytes for another pack's program and cannot restate that base's check metadata.
func (m *Manifest) validateModelListChecks() []string {
	bins := make([]string, 0, len(m.ModelListChecks))
	for bin := range m.ModelListChecks {
		bins = append(bins, bin)
	}
	sort.Strings(bins)
	var problems []string
	for _, bin := range bins {
		label := fmt.Sprintf("model_list_check[%q]", bin)
		owned := false
		for _, c := range m.Contributions() {
			if bin != "" && c.Kind == KindProgram && c.Bin == bin && !c.IsFork() {
				owned = true
				break
			}
		}
		if !owned {
			problems = append(problems, label+": must name a non-fork program declared by this pack; move the fact to the owning pack")
		}
		check := m.ModelListChecks[bin]
		if check == nil {
			problems = append(problems, label+": needs an object; omit the bin to opt out or use {} to opt in")
			continue
		}
		problems = append(problems, modelListCheckProblems(label, check)...)
	}
	return problems
}

// ProgramModelListCheck returns the optional check-only fact from the selected program's
// owning manifest. Delivery rewrites retain the base manifest's map, never a fork's metadata.
func (m *Manifest) ProgramModelListCheck(bin string) (*ModelListCheck, bool) {
	if m == nil || bin == "" {
		return nil, false
	}
	for _, c := range m.Contributions() {
		if c.Kind == KindProgram && c.Bin == bin && !c.IsFork() {
			check := m.ModelListChecks[bin]
			return check, check != nil
		}
	}
	return nil, false
}

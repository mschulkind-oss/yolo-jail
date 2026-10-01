package packdecl

// exactmenu.go is the `exact_menu_refuses` field (Contribution.ExactMenuRefuses carries the
// reasoning; docs/design/model-lists-and-pickers.md MM-D29 the decision): a program whose model
// menu is made exact only by a filter that also refuses. An EXACT MENU is a term
// docs/design/model-lists-and-pickers.md §14 coins: the agent's model menu for a provider offers a
// list's models and no others.

import "fmt"

// ExactMenuRefusal is the body of `exact_menu_refuses`. The field's presence is the fact: this
// program's exact menu refuses, so it is written only while the governing profile's
// `enforce_models` is on. It applies to every list a `models` `only` narrowed (packload's
// `models_only` mark), the lists every agent's derive makes exact where it can.
type ExactMenuRefusal struct {
	// Providers names the providers, by yolo's provider name, whose WHOLE list the program's
	// derive makes its menu, whether or not an `only` narrowed it: packs/opencode names
	// `openai-codex`, whose one list (ML-D1) opencode's derive writes as the `whitelist` of its own
	// `openai` provider whenever the switch is on. Absent when the program's derive narrows only
	// what an `only` narrowed.
	Providers []string `json:"providers,omitempty"`
}

// exactMenuProblems refuses an `exact_menu_refuses` no consumer could read as written: on a kind
// other than program, and a `providers` list that is empty or names a provider twice or as "".
func exactMenuProblems(label string, c Contribution) []string {
	if c.ExactMenuRefuses == nil {
		return nil
	}
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf("%s: kind %q does not take \"exact_menu_refuses\" — it says how a "+
			"PROGRAM's model menu is narrowed, so only \"program\" has a menu to say it of", label, c.Kind)}
	}
	providers := c.ExactMenuRefuses.Providers
	if providers != nil && len(providers) == 0 {
		return []string{fmt.Sprintf("%s: \"exact_menu_refuses.providers\" is an empty list, which names "+
			"no provider — omit it instead", label)}
	}
	var problems []string
	seen := map[string]bool{}
	for _, p := range providers {
		switch {
		case p == "":
			problems = append(problems, fmt.Sprintf("%s: \"exact_menu_refuses.providers\" holds an "+
				"empty provider name", label))
		case seen[p]:
			problems = append(problems, fmt.Sprintf("%s: \"exact_menu_refuses.providers\" names %q twice",
				label, p))
		}
		seen[p] = true
	}
	return problems
}

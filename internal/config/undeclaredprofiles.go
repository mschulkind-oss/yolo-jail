package config

// undeclaredprofiles.go is the ONE declaration check over a profile fold: every selected profile
// name nothing declares, each in the refusal's own words (packload.UndeclaredProfileHelp). The
// jail launch refuses on it (run's checkProfileDeclarations) and `yolo check` predicts that
// refusal from the config alone, so the two cannot disagree about which names are declared or
// what the next step is.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// UndeclaredProfileProblems is one line per selected profile name that neither a pack in packs
// nor userProfiles declares, in fold order: every entry of each agent's active set (AP-D3), then
// every entry of a bare list some receiver took (OQ-AP3 read with AP-D3), each name once.
// keyFrom is the fold's selection index of the config `profile` key, which words a bare list's
// source; universe is the shipped set the refusal names packs from (packload.Embedded()).
// replaced is ReplacedNameRefusals: a name a provider's or variant's `name` replaced is refused in
// those words, naming the name that selects it now (RC-D68). Nil when every name is declared.
func UndeclaredProfileProblems(fold ProfileFold, keyFrom int, userProfiles map[string]packload.UserProfile,
	replaced map[string]string, packs, universe []*packload.Pack) []string {
	help := func(name string, declared []string) string {
		if refusal, ok := replaced[name]; ok {
			return refusal
		}
		return packload.UndeclaredProfileHelp(name, declared, packs, universe)
	}
	if fold.Table == nil || fold.Table.Len() == 0 {
		return nil
	}
	declared := packload.DeclaredProfileNames(packs, userProfiles)
	isDeclared := map[string]bool{}
	for _, name := range declared {
		isDeclared[name] = true
	}
	var problems []string
	reported := map[string]bool{}
	sets := packload.ProfileSets(fold.Table)
	for _, agent := range fold.Table.Keys() {
		for _, name := range sets[agent] {
			if name == "" || isDeclared[name] {
				continue
			}
			reported[name] = true
			problems = append(problems, fmt.Sprintf("profile %q selected for %s: %s",
				name, agent, help(name, declared)))
		}
	}
	if bare := fold.BareList; len(bare) > 1 {
		spelled := "the bare -p list " + strings.Join(bare, ",")
		if fold.BareFrom == keyFrom {
			spelled = "the profile key's list " + strings.Join(bare, ",")
		}
		for i, name := range bare {
			if isDeclared[name] || reported[name] {
				continue
			}
			reported[name] = true
			problems = append(problems, fmt.Sprintf("profile %q (entry %d of %s): %s",
				name, i+1, spelled, help(name, declared)))
		}
	}
	return problems
}

// ConfigProfileFold is the fold a config alone selects over packs: its `profile` key with no
// flag above it, the key at selection index 0 (ConfigProfileTable's fold, kept whole for the
// declaration check).
func ConfigProfileFold(cfg *jsonx.OrderedMap, packs []*packload.Pack) ProfileFold {
	return FoldProfiles(ReceiversOf(packs), ConfigActiveSelection(cfg))
}

package packdecl

// modelcatalog.go is the `model_catalog` field's validation: where, inside the package an npm
// program installs, its agent keeps the model ids it knows (Contribution.ModelCatalog carries the
// reasoning; docs/design/model-lists-and-pickers.md MM-D19 the decision).

import (
	"fmt"
	"path"
	"strings"
)

// modelCatalogProblems refuses a `model_catalog` that could never name a file inside the
// installed package: on a kind or via with no package directory, an empty list, and an entry that
// is empty, absolute, unclean, escaping, backslashed, a duplicate, or not a valid pattern.
func modelCatalogProblems(label string, c Contribution) []string {
	if c.ModelCatalog == nil {
		return nil
	}
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf("%s: kind %q does not take \"model_catalog\" — it names files "+
			"inside the package a PROGRAM installs, so only \"program\" has a package to name them in",
			label, c.Kind)}
	}
	if c.Via != "npm" {
		return []string{fmt.Sprintf("%s: \"model_catalog\" needs \"via\": \"npm\" (this program's "+
			"via is %q) — its entries are relative to the directory npm installs the package "+
			"into, and no other via has one", label, c.Via)}
	}
	if len(c.ModelCatalog) == 0 {
		return []string{fmt.Sprintf("%s: \"model_catalog\" is an empty list, which names no "+
			"catalog — omit the field instead", label)}
	}
	var problems []string
	seen := map[string]bool{}
	for _, g := range c.ModelCatalog {
		if prob := modelCatalogEntryProblem(g); prob != "" {
			problems = append(problems, fmt.Sprintf("%s: \"model_catalog\" entry %q %s", label, g, prob))
			continue
		}
		if seen[g] {
			problems = append(problems, fmt.Sprintf("%s: \"model_catalog\" names %q twice", label, g))
		}
		seen[g] = true
	}
	return problems
}

// modelCatalogEntryProblem says why one entry could never match a file inside the package, ""
// when it could.
func modelCatalogEntryProblem(g string) string {
	switch {
	case g == "":
		return "is empty"
	case g == ".":
		return "names the package's directory itself, not a file in it"
	case strings.Contains(g, `\`):
		return "contains a backslash: entries are slash-separated, and a backslash would be read " +
			"as an escape on one platform and a separator on another"
	case strings.HasPrefix(g, "/"):
		return "is absolute: entries are relative to the installed package's directory"
	case path.Clean(g) != g:
		return "is not clean (write it as " + fmt.Sprintf("%q", path.Clean(g)) + ")"
	}
	for _, seg := range strings.Split(g, "/") {
		if seg == ".." {
			return "leaves the installed package's directory"
		}
		if _, err := path.Match(seg, ""); err != nil {
			return "is not a valid pattern: " + err.Error()
		}
	}
	return ""
}

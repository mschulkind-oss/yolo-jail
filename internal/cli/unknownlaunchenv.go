package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func warnUnknownYOLOEnv(known []string, environ []string, out io.Writer) {
	allowed := make(map[string]bool, len(known))
	for _, name := range known {
		allowed[name] = true
	}
	var unknown []string
	seen := map[string]bool{}
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "YOLO_") || allowed[name] || seen[name] {
			continue
		}
		seen[name] = true
		unknown = append(unknown, name)
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		fmt.Fprintf(out, "yolo: warning: %s is not a recognized launch setting; %s\n", name, unknownYOLOEnvNextStep(name, known))
	}
}

// unknownYOLOEnvNextStep is the warning's next step, chosen to fit the name: a name that reads
// as a runtime choice is pointed at YOLO_SETUP, a near-miss of a recognized setting at that
// setting, and anything else at the reference that documents launch settings.
func unknownYOLOEnvNextStep(name string, known []string) string {
	if looksLikeRuntimeChoice(name) {
		return "unset " + name + ". To choose where it runs, use YOLO_SETUP=podman-self yolo (yolo setup list names every setup)."
	}
	if near := closestKnownYOLOEnv(name, known); near != "" {
		return "did you mean " + near + "? Rename it to " + near + ", or unset " + name + "."
	}
	return "unset " + name + ". Run `yolo config-ref` for the documented launch settings."
}

func looksLikeRuntimeChoice(name string) bool {
	for _, word := range []string{"RUNTIME", "BACKEND", "ENGINE", "PODMAN", "DOCKER"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

// closestKnownYOLOEnv returns the recognized name within a small edit distance of name (a
// typo, a stray plural), or "" when none is close enough to be the likely intent.
func closestKnownYOLOEnv(name string, known []string) string {
	limit := min(2, (len(name)-len("YOLO_"))/4)
	best, bestDist := "", limit+1
	sorted := append([]string(nil), known...)
	sort.Strings(sorted)
	for _, candidate := range sorted {
		if d := editDistance(name, candidate); d < bestDist {
			best, bestDist = candidate, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b, by byte (the names are ASCII).
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func warnUnknownYOLOEnvFromProcess(known []string, out io.Writer) {
	warnUnknownYOLOEnv(known, os.Environ(), out)
}

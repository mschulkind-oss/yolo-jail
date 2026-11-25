// Package releasebaseline selects locally available release tags for compatibility tests.
// It intentionally reads Git refs only: it does not query GitHub or decide publication state.
package releasebaseline

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

var versionPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?$`)

type version struct {
	major, minor, patch string
	pre                 []string
}

type releaseTag struct {
	name    string
	version version
}

// Latest returns the highest valid vX.Y.Z[-prerelease] tag available in the local repo.
func Latest(repo string) (string, error) {
	return selectTag(repo, "", false)
}

// LatestBefore returns the highest valid release tag not pointing at candidate.
// This keeps release-candidate compatibility probes on the previously shipped reader.
func LatestBefore(repo, candidate string) (string, error) {
	return selectTag(repo, candidate, true)
}

// Compare orders two release tags by version: -1, 0 or 1. ok is false when either is not a valid
// vX.Y.Z[-prerelease] tag.
func Compare(a, b string) (c int, ok bool) {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	if !okA || !okB {
		return 0, false
	}
	return compare(va, vb), true
}

func selectTag(repo, candidate string, excludeCandidate bool) (string, error) {
	var candidateCommit string
	if excludeCandidate {
		out, err := git(repo, "rev-parse", "--verify", candidate+"^{commit}")
		if err != nil {
			return "", fmt.Errorf("resolve candidate %q: %w", candidate, err)
		}
		candidateCommit = strings.TrimSpace(string(out))
	}
	out, err := git(repo, "for-each-ref", "--format=%(refname:strip=2)", "refs/tags")
	if err != nil {
		return "", fmt.Errorf("list release tags: %w", err)
	}
	var releases []releaseTag
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		v, ok := parseVersion(name)
		if !ok {
			continue
		}
		commitOut, err := git(repo, "rev-parse", "--verify", name+"^{commit}")
		if err != nil {
			continue // A matching-looking tag that does not peel to a commit is not a release.
		}
		commit := strings.TrimSpace(string(commitOut))
		if excludeCandidate && commit == candidateCommit {
			continue
		}
		releases = append(releases, releaseTag{name: name, version: v})
	}
	if len(releases) == 0 {
		return "", fmt.Errorf("no valid local release tag found")
	}
	sort.Slice(releases, func(i, j int) bool {
		cmp := compare(releases[i].version, releases[j].version)
		if cmp != 0 {
			return cmp > 0
		}
		return releases[i].name < releases[j].name
	})
	return releases[0].name, nil
}

func git(repo string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)), err)
		}
		return nil, err
	}
	return out, nil
}

func parseVersion(tag string) (version, bool) {
	m := versionPattern.FindStringSubmatch(tag)
	if m == nil {
		return version{}, false
	}
	v := version{major: m[1], minor: m[2], patch: m[3]}
	for _, component := range []string{v.major, v.minor, v.patch} {
		if hasLeadingZero(component) {
			return version{}, false
		}
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
		for _, id := range v.pre {
			if id == "" || (isNumeric(id) && hasLeadingZero(id)) {
				return version{}, false
			}
		}
	}
	return v, true
}

func compare(a, b version) int {
	for _, pair := range [][2]string{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if c := compareNumeric(pair[0], pair[1]); c != 0 {
			return c
		}
	}
	if len(a.pre) == 0 || len(b.pre) == 0 {
		switch {
		case len(a.pre) == len(b.pre):
			return 0
		case len(a.pre) == 0:
			return 1
		default:
			return -1
		}
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, yn := isNumeric(x), isNumeric(y)
		switch {
		case xn && yn:
			if c := compareNumeric(x, y); c != 0 {
				return c
			}
		case xn:
			return -1
		case yn:
			return 1
		default:
			if x < y {
				return -1
			} else if x > y {
				return 1
			}
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	} else if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}

func hasLeadingZero(s string) bool {
	return len(s) > 1 && s[0] == '0'
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareNumeric(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	if len(a) < len(b) {
		return -1
	} else if len(a) > len(b) {
		return 1
	} else if a < b {
		return -1
	} else if a > b {
		return 1
	}
	return 0
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var numericIdentifier = regexp.MustCompile(`^[0-9]+$`)

type publishedRelease struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
}

// requireNewestPublishedVersionAllowCurrent refuses an old or duplicate release
// before the one-time claim and before any registry build. Only the publisher
// phase, after it has verified the exact successful original Release run, may
// encounter its own already-published current release. The release-creation path
// must not reuse an existing version/tag.
func requireNewestPublishedVersionAllowCurrent(ctx context.Context, client claimClient, repo, version string, allowCurrent bool) error {
	if !releaseVersionPattern.MatchString(version) {
		return fmt.Errorf("invalid release version %q", version)
	}
	candidate := "v" + version
	currentSeen := false
	for page := 1; ; page++ {
		address := client.apiURL + "/repos/" + repo + "/releases?per_page=100&page=" + fmt.Sprint(page)
		var releases []publishedRelease
		if err := client.getJSON(ctx, address, &releases); err != nil {
			return fmt.Errorf("cannot establish published-version order; no registry publication is allowed: %w", err)
		}
		for _, release := range releases {
			if release.Draft {
				continue
			}
			if release.TagName == candidate {
				if currentSeen {
					return fmt.Errorf("published release list contains duplicate current tag %s; preserve state and inspect it read-only", candidate)
				}
				currentSeen = true
				if !allowCurrent {
					return fmt.Errorf("release %s is already published; duplicate publication is refused", candidate)
				}
				continue
			}
			if !strings.HasPrefix(release.TagName, "v") || !releaseVersionPattern.MatchString(strings.TrimPrefix(release.TagName, "v")) {
				return fmt.Errorf("published release %q has an unrecognized version; preserve it and ask the owner to review ordering", release.TagName)
			}
			comparison, err := compareReleaseVersions(version, strings.TrimPrefix(release.TagName, "v"))
			if err != nil {
				return err
			}
			if comparison <= 0 {
				return fmt.Errorf("release %s is not newer than already published %s; preserve the tag and do not replay older publisher workflows", candidate, release.TagName)
			}
		}
		if len(releases) < 100 {
			return nil
		}
	}
}

// compareReleaseVersions compares the project's X.Y.Z[-pre] version form using
// SemVer precedence without accepting a second tag/version spelling.
func compareReleaseVersions(a, b string) (int, error) {
	if !releaseVersionPattern.MatchString(a) || !releaseVersionPattern.MatchString(b) {
		return 0, fmt.Errorf("cannot compare malformed release versions %q and %q", a, b)
	}
	partsA, partsB := strings.SplitN(a, "-", 2), strings.SplitN(b, "-", 2)
	coreA, coreB := strings.Split(partsA[0], "."), strings.Split(partsB[0], ".")
	for i := 0; i < 3; i++ {
		if comparison := compareNumeric(coreA[i], coreB[i]); comparison != 0 {
			return comparison, nil
		}
	}
	preA, preB := []string(nil), []string(nil)
	if len(partsA) == 2 {
		preA = strings.Split(partsA[1], ".")
	}
	if len(partsB) == 2 {
		preB = strings.Split(partsB[1], ".")
	}
	if len(preA) == 0 && len(preB) == 0 {
		return 0, nil
	}
	if len(preA) == 0 {
		return 1, nil
	}
	if len(preB) == 0 {
		return -1, nil
	}
	for i := 0; i < len(preA) && i < len(preB); i++ {
		aNum, bNum := numericIdentifier.MatchString(preA[i]), numericIdentifier.MatchString(preB[i])
		switch {
		case aNum && bNum:
			if comparison := compareNumeric(preA[i], preB[i]); comparison != 0 {
				return comparison, nil
			}
		case aNum:
			return -1, nil
		case bNum:
			return 1, nil
		default:
			if preA[i] < preB[i] {
				return -1, nil
			}
			if preA[i] > preB[i] {
				return 1, nil
			}
		}
	}
	if len(preA) < len(preB) {
		return -1, nil
	}
	if len(preA) > len(preB) {
		return 1, nil
	}
	return 0, nil
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
	}
	if len(a) > len(b) {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func versionOrderFromEnv(ctx context.Context, out io.Writer) error {
	repo, version := os.Getenv("GITHUB_REPOSITORY"), os.Getenv("RELEASE_VERSION")
	if repo == "" || !releaseRepositoryPattern.MatchString(repo) || strings.Contains(repo, "..") {
		return fmt.Errorf("GITHUB_REPOSITORY must be OWNER/REPO")
	}
	if version == "" || !releaseVersionPattern.MatchString(version) {
		return fmt.Errorf("RELEASE_VERSION must be X.Y.Z or X.Y.Z-pre")
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("a read-only GitHub token is required to verify release order")
	}
	apiURL, err := githubAPIBaseFromEnv()
	if err != nil {
		return err
	}
	client := claimClient{apiURL: apiURL, token: token, http: newGitHubHTTPClient()}
	if err := requireNewestPublishedVersionAllowCurrent(ctx, client, repo, version, os.Getenv("RELEASE_ORDER_ALLOW_CURRENT") == "1"); err != nil {
		return err
	}
	fmt.Fprintf(out, "Version v%s is newer than every existing published release.\n", version)
	return nil
}

const defaultAPITimeout = 30 * time.Second

func githubAPIBaseFromEnv() (string, error) {
	base := strings.TrimRight(os.Getenv("GITHUB_API_URL"), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("GITHUB_API_URL must be an HTTPS URL without credentials, query, or fragment")
	}
	return base, nil
}

func newGitHubHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultAPITimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

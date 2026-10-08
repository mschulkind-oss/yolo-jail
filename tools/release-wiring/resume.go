package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// A resume continues a release request whose tag was created but whose
// publication never started: the tag exists and peels to the requested commit,
// and nothing after the tag left any state. It skips tag creation and goes on
// from the Release dispatch under a fresh request run. Every precondition is
// read-only and each one refuses rather than guessing; see
// docs/design/pre-tag-release-gate.md, "Resuming a tag-only release".

const (
	pypiProjectURL     = "https://pypi.org/pypi/yolo-jail/json"
	homebrewFormulaURL = "https://raw.githubusercontent.com/mschulkind-oss/homebrew-tap/main/Formula/yolo-jail.rb"
)

// A run listing costs about 15 KB a run and a release about 10 KB, against
// getJSON's 1 MiB cap, so pages stay small enough to never truncate.
const resumePageSize = 30

type resumeSources struct {
	github     claimClient
	public     *http.Client
	pypiURL    string
	formulaURL string
}

type resumeRun struct {
	ID          int64  `json:"id"`
	Event       string `json:"event"`
	HeadBranch  string `json:"head_branch"`
	DisplayName string `json:"display_title"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
}

func verifyResume(ctx context.Context, src resumeSources, repo, version, sha, requestID string) error {
	if !releaseRepositoryPattern.MatchString(repo) || strings.Contains(repo, "..") ||
		!releaseVersionPattern.MatchString(version) || !releaseCommitSHA.MatchString(sha) ||
		!provenanceRunID.MatchString(requestID) || parseRunID(requestID) <= 0 {
		return fmt.Errorf("resume repository, version, SHA, or run id is malformed")
	}
	tag := "v" + version
	gh := src.github

	// 1. The tag is the annotated tag a request creates, at exactly this commit.
	var ref gitRef
	if err := gh.getJSON(ctx, gh.apiURL+"/repos/"+repo+"/git/ref/tags/"+url.PathEscape(tag), &ref); err != nil {
		return fmt.Errorf("cannot read %s: %w", tag, err)
	}
	if ref.Object.Type != "tag" || !releaseCommitSHA.MatchString(ref.Object.SHA) {
		return fmt.Errorf("%s is not an annotated tag (object type %q); only a tag a release request created can resume", tag, ref.Object.Type)
	}
	var annotated gitTag
	if err := gh.getJSON(ctx, gh.apiURL+"/repos/"+repo+"/git/tags/"+ref.Object.SHA, &annotated); err != nil {
		return fmt.Errorf("cannot read the %s tag object: %w", tag, err)
	}
	if strings.TrimSpace(annotated.Message) != "yolo-jail "+version {
		return fmt.Errorf("%s has message %q, not the %q a release request writes; only a request's own tag can resume", tag, strings.TrimSpace(annotated.Message), "yolo-jail "+version)
	}
	if annotated.Object.Type != "commit" || annotated.Object.SHA != sha {
		return fmt.Errorf("%s peels to %s %s, not the requested commit %s; the tag is never moved, so this version cannot resume at that commit", tag, annotated.Object.Type, annotated.Object.SHA, sha)
	}

	// 2. No GitHub Release for the tag, draft or published. Drafts are listed
	// only to a token with write access, which the request's write job holds;
	// the release assets and the publication claim live only on a release.
	for page := 1; ; page++ {
		var releases []claimRelease
		if err := gh.getJSON(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", gh.apiURL, repo, resumePageSize, page), &releases); err != nil {
			return fmt.Errorf("cannot list releases: %w", err)
		}
		for _, release := range releases {
			if release.TagName == tag {
				kind := "published"
				if release.Draft {
					kind = "draft"
				}
				return fmt.Errorf("a %s GitHub Release exists for %s (id %d); a release, its assets or its claim may be partial publication", kind, tag, release.ID)
			}
		}
		if len(releases) < resumePageSize {
			break
		}
	}

	// 3. Every earlier Release run for this version failed, none is still
	// running, and none was a Homebrew backfill or a tag-push run.
	releaseRuns, err := workflowRuns(ctx, gh, repo, ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	for _, run := range releaseRuns {
		normal := strings.HasPrefix(run.DisplayName, "Release "+tag+" @ ")
		if run.DisplayName == "Homebrew-only "+tag || run.HeadBranch == tag {
			return fmt.Errorf("release.yml run %d (%q) wrote or could write the tap for %s", run.ID, run.DisplayName, tag)
		}
		if !normal {
			continue
		}
		if !strings.HasPrefix(run.DisplayName, "Release "+tag+" @ "+sha+" / request ") {
			return fmt.Errorf("release.yml run %d (%q) targeted another commit for %s", run.ID, run.DisplayName, tag)
		}
		if run.Status != "completed" {
			return fmt.Errorf("release.yml run %d for %s is still %s; wait for it to finish", run.ID, tag, run.Status)
		}
		switch run.Conclusion {
		case "success":
			return fmt.Errorf("release.yml run %d for %s succeeded, so publication already started", run.ID, tag)
		case "failure", "cancelled", "timed_out", "action_required", "neutral", "skipped", "stale", "startup_failure":
		default:
			return fmt.Errorf("release.yml run %d has conclusion %q, so unsuccessful completion is unproven", run.ID, run.Conclusion)
		}
	}

	// 4. No publisher run for this version ever existed: it claims and then
	// writes PyPI, Cachix and GHCR.
	publishRuns, err := workflowRuns(ctx, gh, repo, ".github/workflows/publish.yml")
	if err != nil {
		return err
	}
	for _, run := range publishRuns {
		if strings.HasPrefix(run.DisplayName, "Publish "+tag+" @ ") || run.HeadBranch == tag {
			return fmt.Errorf("publisher run %d (%q) exists for %s", run.ID, run.DisplayName, tag)
		}
	}

	// 5. No other release request for this version is still running.
	requestRuns, err := workflowRuns(ctx, gh, repo, ".github/workflows/release-request.yml")
	if err != nil {
		return err
	}
	for _, run := range requestRuns {
		if strings.HasPrefix(run.DisplayName, "Release request "+tag+" @ ") && run.ID != parseRunID(requestID) && run.Status != "completed" {
			return fmt.Errorf("release request run %d for %s is still %s", run.ID, tag, run.Status)
		}
	}

	// 6. PyPI has no such version.
	body, status, err := publicGet(ctx, src.public, src.pypiURL, 16<<20)
	if err != nil {
		return fmt.Errorf("cannot read PyPI: %w", err)
	}
	switch status {
	case http.StatusNotFound:
	case http.StatusOK:
		var project struct {
			Releases map[string]json.RawMessage `json:"releases"`
		}
		if err := json.Unmarshal(body, &project); err != nil || project.Releases == nil {
			return fmt.Errorf("cannot read PyPI's release list for yolo-jail")
		}
		for published := range project.Releases {
			if pep440Loose(published) == pep440Loose(version) {
				return fmt.Errorf("PyPI already has yolo-jail %s", published)
			}
		}
	default:
		return fmt.Errorf("PyPI returned HTTP %d, so the version's absence is unproven", status)
	}

	// 7. The Homebrew tap's formula does not carry this version.
	body, status, err = publicGet(ctx, src.public, src.formulaURL, 1<<20)
	if err != nil {
		return fmt.Errorf("cannot read the Homebrew formula: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("the Homebrew formula returned HTTP %d, so the tap's state is unproven", status)
	}
	formulaTags := regexp.MustCompile(`(?m)^\s*url\s+"https://github\.com/[^/"\s]+/[^/"\s]+/archive/refs/tags/(v[^/"\s]+)\.tar\.gz"`).FindAllStringSubmatch(string(body), -1)
	if len(formulaTags) != 1 {
		return fmt.Errorf("the Homebrew formula has no single recognizable tag URL, so the tap's state is unproven")
	}
	if formulaTags[0][1] == tag {
		return fmt.Errorf("the Homebrew tap's formula already carries %s", tag)
	}
	return nil
}

func workflowRuns(ctx context.Context, gh claimClient, repo, path string) ([]resumeRun, error) {
	workflow, err := workflowForPath(ctx, gh, repo, path)
	if err != nil {
		return nil, err
	}
	var all []resumeRun
	var expected *int
	for page := 1; page <= 1000; page++ {
		var response struct {
			Total *int        `json:"total_count"`
			Runs  []resumeRun `json:"workflow_runs"`
		}
		address := fmt.Sprintf("%s/repos/%s/actions/workflows/%d/runs?per_page=%d&page=%d", gh.apiURL, repo, workflow.ID, resumePageSize, page)
		if err := gh.getJSON(ctx, address, &response); err != nil {
			return nil, fmt.Errorf("cannot list %s runs: %w", path, err)
		}
		if response.Total == nil || *response.Total < 0 || response.Runs == nil {
			return nil, fmt.Errorf("%s run listing is missing its count or array, so its completeness is unproven", path)
		}
		if expected == nil {
			expected = response.Total
		} else if *expected != *response.Total {
			return nil, fmt.Errorf("%s run count changed during pagination, so its completeness is unproven", path)
		}
		all = append(all, response.Runs...)
		if len(all) > *expected || len(response.Runs) > resumePageSize {
			return nil, fmt.Errorf("%s run pagination is inconsistent, so its completeness is unproven", path)
		}
		if len(all) == *expected {
			return all, nil
		}
		if len(response.Runs) < resumePageSize {
			return nil, fmt.Errorf("%s run pagination was incomplete", path)
		}
	}
	return nil, fmt.Errorf("%s run pagination exceeded the safety limit; inspect the history before resuming", path)
}

func publicGet(ctx context.Context, client *http.Client, address string, limit int64) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(body)) > limit {
		return nil, 0, fmt.Errorf("response from %s is larger than %d bytes", address, limit)
	}
	return body, response.StatusCode, nil
}

// pep440Loose folds the spellings PEP 440 treats as one version: case, the
// separators around a pre-release label, and the label's long forms (alpha,
// beta, c, pre, preview), so 0.13.0-alpha.1 matches 0.13.0a1.
func pep440Loose(version string) string {
	version = strings.ToLower(strings.TrimPrefix(version, "v"))
	core, rest, found := strings.Cut(version, "-")
	if !found {
		for i, r := range version {
			if r != '.' && (r < '0' || r > '9') {
				core, rest = version[:i], version[i:]
				break
			}
		}
	}
	rest = strings.NewReplacer("-", "", ".", "", "_", "").Replace(rest)
	for _, label := range []struct{ long, short string }{{"preview", "rc"}, {"alpha", "a"}, {"beta", "b"}, {"pre", "rc"}, {"c", "rc"}} {
		if strings.HasPrefix(rest, label.long) && !strings.HasPrefix(rest, "rc") {
			rest = label.short + strings.TrimPrefix(rest, label.long)
			break
		}
	}
	return core + rest
}

func verifyResumeFromEnv(ctx context.Context, out io.Writer) error {
	repo, version, sha, requestID := os.Getenv("GITHUB_REPOSITORY"), os.Getenv("RELEASE_VERSION"), os.Getenv("RELEASE_SHA"), os.Getenv("GITHUB_RUN_ID")
	if os.Getenv("GITHUB_REF_TYPE") != "branch" || os.Getenv("GITHUB_REF_NAME") != "main" || os.Getenv("GITHUB_RUN_ATTEMPT") != "1" {
		return fmt.Errorf("a resume must run on the first attempt of a main-scoped release request")
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		return fmt.Errorf("the release request's write token is required, since only it lists draft releases")
	}
	apiURL, err := githubAPIBaseFromEnv()
	if err != nil {
		return err
	}
	src := resumeSources{
		github:     claimClient{apiURL: apiURL, token: token, http: newGitHubHTTPClient()},
		public:     newGitHubHTTPClient(),
		pypiURL:    pypiProjectURL,
		formulaURL: homebrewFormulaURL,
	}
	if err := verifyResume(ctx, src, repo, version, sha, requestID); err != nil {
		return err
	}
	fmt.Fprintf(out, "v%s exists at %s and nothing after the tag left any state; resuming from the Release dispatch.\n", version, sha)
	return nil
}

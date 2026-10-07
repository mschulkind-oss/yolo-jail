package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	workflowPath = ".github/workflows/ci.yml"
	branch       = "main"
	event        = "push"
	pageSize     = 100
	maxPages     = 1000
)

type gate struct {
	repo       string
	sha        string
	token      string
	client     *http.Client
	baseURL    string
	now        func() time.Time
	sleep      func(context.Context, time.Duration) error
	timeout    time.Duration
	pollEvery  time.Duration
	requestMax time.Duration
	deadline   time.Time
}

type proof struct {
	Repository   string `json:"repository"`
	SHA          string `json:"sha"`
	WorkflowID   int64  `json:"workflow_id"`
	WorkflowPath string `json:"workflow_path"`
	Branch       string `json:"branch"`
	Event        string `json:"event"`
	RunID        int64  `json:"run_id"`
	RunNumber    int64  `json:"run_number"`
	RunAttempt   int    `json:"run_attempt"`
	HTMLURL      string `json:"html_url"`
	Conclusion   string `json:"conclusion"`
}

type workflow struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

type repository struct {
	FullName string `json:"full_name"`
}

type run struct {
	ID             int64      `json:"id"`
	RunNumber      int64      `json:"run_number"`
	RunAttempt     int        `json:"run_attempt"`
	WorkflowID     int64      `json:"workflow_id"`
	Path           string     `json:"path"`
	HeadSHA        string     `json:"head_sha"`
	HeadBranch     string     `json:"head_branch"`
	Event          string     `json:"event"`
	Status         string     `json:"status"`
	Conclusion     string     `json:"conclusion"`
	HTMLURL        string     `json:"html_url"`
	Repository     repository `json:"repository"`
	HeadRepository repository `json:"head_repository"`
}

type runList struct {
	TotalCount   int   `json:"total_count"`
	WorkflowRuns []run `json:"workflow_runs"`
}

type job struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

type jobList struct {
	TotalCount int   `json:"total_count"`
	Jobs       []job `json:"jobs"`
}

func (g *gate) wait(parent context.Context) (proof, error) {
	if g.client == nil || g.now == nil || g.sleep == nil || g.timeout <= 0 || g.pollEvery <= 0 || g.requestMax <= 0 {
		return proof{}, errors.New("release gate is missing valid runtime configuration")
	}
	ctx, cancel := context.WithTimeout(parent, g.timeout)
	defer cancel()
	deadline := g.now().Add(g.timeout)
	api := *g
	api.deadline = deadline
	api.client = safeHTTPClient(g.client)

	wf, err := api.getWorkflow(ctx)
	if err != nil {
		return proof{}, err
	}
	if wf.ID <= 0 || wf.Path != workflowPath {
		return proof{}, errors.New("the Actions workflow identity did not match the required CI workflow")
	}

	for {
		if err := api.checkDeadline(ctx, deadline); err != nil {
			return proof{}, err
		}
		runs, err := api.listRuns(ctx, wf.ID)
		if err != nil {
			return proof{}, err
		}
		latest := newestEligible(runs, g.repo, g.sha, wf.ID)
		if latest != nil {
			current, err := api.getRun(ctx, latest.ID)
			if err != nil {
				return proof{}, err
			}
			if !eligible(current, g.repo, g.sha, wf.ID) || current.RunNumber != latest.RunNumber {
				return proof{}, errors.New("selected Actions run changed identity or no longer proves the requested commit")
			}
			switch current.Status {
			case "queued", "in_progress", "waiting", "pending", "requested":
				// Keep polling the newest matching run; an older green run cannot substitute.
			case "completed":
				if current.Conclusion != "success" {
					return proof{}, errors.New("latest matching CI run did not conclude success")
				}
				if current.RunAttempt < 1 {
					return proof{}, errors.New("completed CI run has no valid attempt number")
				}
				if err := api.requireJobs(ctx, current.ID, current.RunAttempt); err != nil {
					return proof{}, err
				}
				// Close the rerun/newer-run window as far as an API observation can: refresh
				// both the candidate list and the selected run after reading attempt jobs.
				freshRuns, err := api.listRuns(ctx, wf.ID)
				if err != nil {
					return proof{}, err
				}
				freshest := newestEligible(freshRuns, g.repo, g.sha, wf.ID)
				freshRun, err := api.getRun(ctx, current.ID)
				if err != nil {
					return proof{}, err
				}
				if freshest == nil || freshest.ID != current.ID || freshest.RunNumber != current.RunNumber ||
					!eligible(freshRun, g.repo, g.sha, wf.ID) || freshRun.Status != "completed" ||
					freshRun.Conclusion != "success" || freshRun.RunAttempt != current.RunAttempt {
					// Re-evaluate instead of accepting an attempt whose identity changed.
					continue
				}
				if !safeRunURL(freshRun.HTMLURL, g.repo, freshRun.ID) {
					return proof{}, errors.New("the Actions API returned an invalid run URL")
				}
				return proof{
					Repository: g.repo, SHA: g.sha, WorkflowID: wf.ID, WorkflowPath: workflowPath,
					Branch: branch, Event: event, RunID: freshRun.ID, RunNumber: freshRun.RunNumber,
					RunAttempt: freshRun.RunAttempt, HTMLURL: freshRun.HTMLURL, Conclusion: freshRun.Conclusion,
				}, nil
			default:
				return proof{}, errors.New("latest matching CI run has an unrecognized status")
			}
		}

		if err := api.checkDeadline(ctx, deadline); err != nil {
			return proof{}, err
		}
		remaining := deadline.Sub(g.now())
		pause := g.pollEvery
		if remaining < pause {
			pause = remaining
		}
		if pause <= 0 {
			return proof{}, errors.New("timed out waiting for exact-commit CI proof")
		}
		if err := g.sleep(ctx, pause); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return proof{}, errors.New("timed out waiting for exact-commit CI proof")
			}
			return proof{}, fmt.Errorf("waiting for CI was interrupted: %w", err)
		}
	}
}

func safeHTTPClient(client *http.Client) *http.Client {
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}

func (g *gate) checkDeadline(ctx context.Context, deadline time.Time) error {
	if ctx.Err() != nil || !g.now().Before(deadline) {
		return errors.New("timed out waiting for exact-commit CI proof")
	}
	return nil
}

func (g *gate) getWorkflow(ctx context.Context) (workflow, error) {
	var out workflow
	_, err := g.getJSON(ctx, "/repos/"+g.repo+"/actions/workflows/ci.yml", nil, &out)
	return out, err
}

func (g *gate) listRuns(ctx context.Context, workflowID int64) ([]run, error) {
	endpoint := fmt.Sprintf("/repos/%s/actions/workflows/%d/runs", g.repo, workflowID)
	query := url.Values{"event": {event}, "branch": {branch}, "head_sha": {g.sha}, "per_page": {strconv.Itoa(pageSize)}}
	var all []run
	for page := 1; page <= maxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		var result runList
		next, err := g.getJSON(ctx, endpoint, query, &result)
		if err != nil {
			return nil, err
		}
		all = append(all, result.WorkflowRuns...)
		if next == nil {
			if result.TotalCount != len(all) {
				return nil, errors.New("the Actions run pagination was incomplete")
			}
			return all, nil
		}
		query = next
	}
	return nil, errors.New("the Actions run pagination exceeded the safety limit")
}

func (g *gate) getRun(ctx context.Context, id int64) (run, error) {
	var out run
	_, err := g.getJSON(ctx, fmt.Sprintf("/repos/%s/actions/runs/%d", g.repo, id), nil, &out)
	return out, err
}

func (g *gate) requireJobs(ctx context.Context, runID int64, attempt int) error {
	endpoint := fmt.Sprintf("/repos/%s/actions/runs/%d/attempts/%d/jobs", g.repo, runID, attempt)
	query := url.Values{"per_page": {strconv.Itoa(pageSize)}}
	var all []job
	for page := 1; page <= maxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		var result jobList
		next, err := g.getJSON(ctx, endpoint, query, &result)
		if err != nil {
			return err
		}
		all = append(all, result.Jobs...)
		if next == nil {
			if result.TotalCount != len(all) {
				return errors.New("the Actions job pagination was incomplete")
			}
			break
		}
		query = next
		if page == maxPages {
			return errors.New("the Actions job pagination exceeded the safety limit")
		}
	}
	if len(all) == 0 {
		return errors.New("completed CI run returned no jobs")
	}
	families := map[string]map[string]bool{}
	for _, item := range all {
		if item.Conclusion != "success" {
			return errors.New("a CI job did not conclude success")
		}
		family := jobFamily(item.Name)
		if family != "" {
			if families[family] == nil {
				families[family] = map[string]bool{}
			}
			families[family][item.Name] = true
		}
	}
	for _, required := range []string{"secrets-scan", "check-go", "check-macos", "build-image", "integration", "integration-complete"} {
		if len(families[required]) == 0 {
			return fmt.Errorf("completed CI run is missing required job family %q", required)
		}
	}
	if len(families["build-image"]) < 2 {
		return errors.New("completed CI run is missing a required build-image architecture job")
	}
	if len(families["integration"]) < 4 {
		return errors.New("completed CI run is missing a required integration architecture/shard job")
	}
	return nil
}

func (g *gate) getJSON(ctx context.Context, endpoint string, query url.Values, dst any) (url.Values, error) {
	u, err := url.Parse(strings.TrimRight(g.baseURL, "/") + endpoint)
	if err != nil {
		return nil, errors.New("could not construct Actions API URL")
	}
	if query != nil {
		u.RawQuery = query.Encode()
	}
	for attempt := 0; attempt <= 3; attempt++ {
		if err := g.checkDeadline(ctx, g.deadline); err != nil {
			return nil, err
		}
		requestContext, cancel := context.WithTimeout(ctx, g.requestMax)
		req, err := http.NewRequestWithContext(requestContext, http.MethodGet, u.String(), nil)
		if err != nil {
			cancel()
			return nil, errors.New("could not construct Actions API request")
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if g.token != "" {
			req.Header.Set("Authorization", "Bearer "+g.token)
		}
		response, err := g.client.Do(req)
		if err != nil {
			cancel()
			if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
				return nil, errors.New("the Actions API request timed out")
			}
			return nil, errors.New("the Actions API request failed")
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			delay := retryDelay(response.Header.Get("Retry-After"), attempt, g.now())
			response.Body.Close()
			cancel()
			if attempt == 3 {
				return nil, fmt.Errorf("the Actions API remained unavailable (HTTP %d) for %s", response.StatusCode, endpoint)
			}
			remaining := g.deadline.Sub(g.now())
			if remaining <= 0 {
				return nil, errors.New("timed out waiting for exact-commit CI proof")
			}
			if delay > remaining {
				delay = remaining
			}
			if err := g.sleep(ctx, delay); err != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return nil, errors.New("timed out waiting for exact-commit CI proof")
				}
				return nil, errors.New("the Actions API retry was interrupted")
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			cancel()
			return nil, fmt.Errorf("the Actions API returned HTTP %d for %s", response.StatusCode, endpoint)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (5<<20)+1))
		response.Body.Close()
		if err != nil {
			cancel()
			return nil, errors.New("could not read Actions API response")
		}
		if len(body) > 5<<20 {
			cancel()
			return nil, errors.New("the Actions API response exceeded the size limit")
		}
		if err := json.Unmarshal(body, dst); err != nil {
			cancel()
			return nil, errors.New("the Actions API returned invalid JSON")
		}
		next, err := nextPage(response.Header, u, endpoint, query)
		cancel()
		if err != nil {
			return nil, err
		}
		return next, nil
	}
	return nil, errors.New("the Actions API retry limit reached")
}

func retryDelay(retryAfter string, attempt int, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(retryAfter); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return time.Second << attempt
}

func runWorkflowPath(path string) bool {
	return path == workflowPath || strings.HasPrefix(path, workflowPath+"@")
}

var nextLinkPattern = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="?([^";, ]+)"?`)

func nextPage(header http.Header, current *url.URL, expectedPath string, expectedQuery url.Values) (url.Values, error) {
	links := nextLinkPattern.FindAllStringSubmatch(header.Get("Link"), -1)
	for _, link := range links {
		if link[2] != "next" {
			continue
		}
		next, err := url.Parse(link[1])
		if err != nil {
			return nil, errors.New("the Actions API returned invalid pagination link")
		}
		next = current.ResolveReference(next)
		if next.Scheme != current.Scheme || next.Host != current.Host || next.EscapedPath() != current.EscapedPath() || next.Path != expectedPath {
			return nil, errors.New("the Actions API pagination link changed origin or endpoint")
		}
		values, err := url.ParseQuery(next.RawQuery)
		if err != nil {
			return nil, errors.New("the Actions API returned invalid pagination query")
		}
		page, pageErr := strconv.Atoi(values.Get("page"))
		currentPage, currentPageErr := strconv.Atoi(expectedQuery.Get("page"))
		if pageErr != nil || currentPageErr != nil || page != currentPage+1 {
			return nil, errors.New("the Actions API returned invalid pagination page")
		}
		if len(values) != len(expectedQuery) {
			return nil, errors.New("the Actions API pagination link changed its query filters")
		}
		for key, expected := range expectedQuery {
			if key == "page" {
				continue
			}
			actual := values[key]
			if len(actual) != len(expected) {
				return nil, errors.New("the Actions API pagination link changed its query filters")
			}
			for i := range expected {
				if actual[i] != expected[i] {
					return nil, errors.New("the Actions API pagination link changed its query filters")
				}
			}
		}
		if values.Get("per_page") != strconv.Itoa(pageSize) || expectedPath == "" {
			return nil, errors.New("the Actions API returned unexpected pagination parameters")
		}
		return values, nil
	}
	if strings.TrimSpace(header.Get("Link")) != "" && len(links) == 0 {
		return nil, errors.New("the Actions API returned malformed pagination metadata")
	}
	return nil, nil
}

func eligible(candidate run, repo, sha string, workflowID int64) bool {
	return candidate.ID > 0 && candidate.RunNumber > 0 && candidate.WorkflowID == workflowID &&
		runWorkflowPath(candidate.Path) && candidate.HeadSHA == sha &&
		candidate.HeadBranch == branch && candidate.Event == event &&
		candidate.Repository.FullName == repo && candidate.HeadRepository.FullName == repo
}

func newestEligible(runs []run, repo, sha string, workflowID int64) *run {
	eligibleRuns := make([]run, 0, len(runs))
	for _, candidate := range runs {
		if eligible(candidate, repo, sha, workflowID) {
			eligibleRuns = append(eligibleRuns, candidate)
		}
	}
	if len(eligibleRuns) == 0 {
		return nil
	}
	sort.Slice(eligibleRuns, func(i, j int) bool {
		if eligibleRuns[i].RunNumber == eligibleRuns[j].RunNumber {
			return eligibleRuns[i].ID > eligibleRuns[j].ID
		}
		return eligibleRuns[i].RunNumber > eligibleRuns[j].RunNumber
	})
	return &eligibleRuns[0]
}

func jobFamily(name string) string {
	for _, family := range []string{"secrets-scan", "check-go", "check-macos", "build-image", "integration", "integration-complete"} {
		if name == family || strings.HasPrefix(name, family+" (") {
			return family
		}
	}
	return ""
}

func safeRunURL(value, repo string, runID int64) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" &&
		u.Fragment == "" && u.Path == "/"+repo+"/actions/runs/"+strconv.FormatInt(runID, 10)
}

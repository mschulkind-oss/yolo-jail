package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

func TestWaitReturnsExactCommitProof(t *testing.T) {
	api := newFakeAPI()
	api.runs = []run{testRun(42, 3, "completed", "success")}
	api.details[42] = []run{testRun(42, 3, "completed", "success")}
	g, _ := newTestGate(t, api)
	got, err := g.wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got.SHA != testSHA || got.Repository != "owner/project" || got.RunID != 42 || got.RunAttempt != 3 || got.Conclusion != "success" {
		t.Fatalf("proof = %+v, want exact SHA/repository and latest successful run 42 attempt 3", got)
	}
	if api.jobAttempts[3] == 0 {
		t.Fatal("latest attempt's jobs were not queried")
	}
	if api.queryMismatch {
		t.Fatal("run listing omitted or changed required exact-SHA push filters")
	}
}

func TestNewestEligibleUsesRunNumberThenRunID(t *testing.T) {
	low := testRun(130, 1, "completed", "success")
	higherNumber := testRun(131, 1, "queued", "")
	higherID := testRun(132, 1, "queued", "")
	low.RunNumber = 10
	higherNumber.RunNumber = 11
	higherID.RunNumber = 11
	got := newestEligible([]run{higherNumber, low, higherID}, "owner/project", testSHA, 9)
	if got == nil || got.ID != higherID.ID {
		t.Fatalf("newest candidate=%+v, want max run_number then max run ID", got)
	}
}

func TestWaitsForQueuedRunThenAcceptsSuccess(t *testing.T) {
	api := newFakeAPI()
	candidate := testRun(43, 1, "queued", "")
	api.runs = []run{candidate}
	api.details[43] = []run{candidate, testRun(43, 1, "in_progress", ""), testRun(43, 1, "completed", "success")}
	g, clock := newTestGate(t, api)
	got, err := g.wait(context.Background())
	if err != nil || got.RunID != 43 {
		t.Fatalf("wait = %+v, %v", got, err)
	}
	if clock.sleeps < 2 {
		t.Fatalf("slept %d times, want polling across queued/running states", clock.sleeps)
	}
}

func TestLatestMatchingRunNeverFallsBackToOlderSuccess(t *testing.T) {
	for _, status := range []string{"queued", "completed"} {
		t.Run(status, func(t *testing.T) {
			api := newFakeAPI()
			api.runs = []run{
				testRun(51, 1, "completed", "success"),
				testRun(52, 1, status, map[bool]string{true: "failure", false: ""}[status == "completed"]),
			}
			api.details[52] = []run{api.runs[1]}
			g, _ := newTestGate(t, api)
			_, err := g.wait(context.Background())
			if err == nil {
				t.Fatal("accepted older green run despite newer matching run")
			}
			if api.detailCalls[51] != 0 {
				t.Fatal("queried or fell back to the older run")
			}
		})
	}
}

func TestCompletedNonSuccessConclusionsRefuse(t *testing.T) {
	for _, conclusion := range []string{"failure", "cancelled", "timed_out", "action_required", "skipped", "neutral", "unknown"} {
		t.Run(conclusion, func(t *testing.T) {
			api := newFakeAPI()
			api.runs = []run{testRun(60, 1, "completed", conclusion)}
			api.details[60] = []run{api.runs[0]}
			g, _ := newTestGate(t, api)
			if _, err := g.wait(context.Background()); err == nil {
				t.Fatalf("accepted conclusion %q", conclusion)
			}
			if api.jobCalls != 0 {
				t.Fatal("queried jobs for a failed/cancelled run")
			}
		})
	}
}

func TestMissingRunTimesOutWithoutProof(t *testing.T) {
	api := newFakeAPI()
	g, clock := newTestGate(t, api)
	g.timeout = 11 * time.Second
	g.pollEvery = 5 * time.Second
	if _, err := g.wait(context.Background()); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("wait error = %v, want timeout", err)
	}
	if clock.elapsed < 11*time.Second || api.runListCalls < 2 {
		t.Fatalf("elapsed=%s list calls=%d, want bounded polling to timeout", clock.elapsed, api.runListCalls)
	}
}

func TestWrongSHAWorkflowEventBranchAndRepositoryNeverQualify(t *testing.T) {
	mutations := map[string]func(*run){
		"wrong-sha":      func(r *run) { r.HeadSHA = strings.Repeat("f", 40) },
		"wrong-workflow": func(r *run) { r.WorkflowID++ },
		"wrong-path":     func(r *run) { r.Path = ".github/workflows/other.yml@refs/heads/main" },
		"pull-request":   func(r *run) { r.Event = "pull_request" },
		"feature-branch": func(r *run) { r.HeadBranch = "feature" },
		"fork":           func(r *run) { r.HeadRepository.FullName = "fork/project" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			api := newFakeAPI()
			candidate := testRun(70, 1, "completed", "success")
			mutate(&candidate)
			api.runs = []run{candidate}
			api.details[70] = []run{candidate}
			g, _ := newTestGate(t, api)
			g.timeout = time.Second
			g.pollEvery = time.Second
			if _, err := g.wait(context.Background()); err == nil {
				t.Fatal("accepted mismatched candidate")
			}
			if api.detailCalls[70] != 0 {
				t.Fatal("fetched details for an ineligible candidate")
			}
		})
	}
}

func TestWorkflowIdentityMustMatchPath(t *testing.T) {
	api := newFakeAPI()
	api.workflow = workflow{ID: 9, Path: ".github/workflows/other.yml"}
	g, _ := newTestGate(t, api)
	if _, err := g.wait(context.Background()); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wait error = %v, want workflow identity refusal", err)
	}
}

func TestLatestAttemptRerunInvalidatesEarlierJobs(t *testing.T) {
	api := newFakeAPI()
	good := testRun(80, 1, "completed", "success")
	api.runs = []run{good}
	api.details[80] = []run{good, testRun(80, 2, "completed", "success"), testRun(80, 2, "completed", "success"), testRun(80, 2, "completed", "success")}
	g, _ := newTestGate(t, api)
	proof, err := g.wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if proof.RunAttempt != 2 || api.jobAttempts[1] == 0 || api.jobAttempts[2] == 0 {
		t.Fatalf("proof=%+v attempts queried=%v, want attempt 2 and both attempt job reads", proof, api.jobAttempts)
	}
}

func TestRunAndJobPaginationAreConsumed(t *testing.T) {
	api := newFakeAPI()
	api.runsPages = map[int]runList{
		1: {TotalCount: 2, WorkflowRuns: []run{testRun(90, 1, "completed", "success")}},
		2: {TotalCount: 2, WorkflowRuns: []run{testRun(91, 1, "completed", "success")}},
	}
	api.details[91] = []run{testRun(91, 1, "completed", "success")}
	api.paginateJobs = true
	g, _ := newTestGate(t, api)
	got, err := g.wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got.RunID != 91 || api.runPageCalls[2] < 2 || api.jobPageCalls[1] < 2 {
		t.Fatalf("proof=%+v run pages=%v job pages=%v", got, api.runPageCalls, api.jobPageCalls)
	}
}

func TestMissingRequiredOrFailedJobsRefuse(t *testing.T) {
	for _, mode := range []string{"missing-integration-complete", "failed-integration", "empty", "duplicate-matrix"} {
		t.Run(mode, func(t *testing.T) {
			api := newFakeAPI()
			api.runs = []run{testRun(100, 1, "completed", "success")}
			api.details[100] = []run{api.runs[0]}
			api.jobMode = mode
			g, _ := newTestGate(t, api)
			if _, err := g.wait(context.Background()); err == nil {
				t.Fatal("accepted incomplete/failed job proof")
			}
		})
	}
}

func TestHTTPFailureAndIncompletePaginationFailClosed(t *testing.T) {
	for _, mode := range []string{"http-500", "incomplete-runs", "incomplete-jobs"} {
		t.Run(mode, func(t *testing.T) {
			api := newFakeAPI()
			api.runs = []run{testRun(110, 1, "completed", "success")}
			api.details[110] = []run{api.runs[0]}
			api.failureMode = mode
			g, _ := newTestGate(t, api)
			if _, err := g.wait(context.Background()); err == nil {
				t.Fatal("accepted HTTP/pagination failure")
			}
		})
	}
}

func TestPerRequestTimeoutIsBounded(t *testing.T) {
	api := newFakeAPI()
	api.blockWorkflow = true
	g, _ := newTestGate(t, api)
	g.requestMax = 10 * time.Millisecond
	g.timeout = time.Second
	start := time.Now()
	if _, err := g.wait(context.Background()); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("wait error = %v, want request timeout", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("per-request timeout was not enforced")
	}
}

func TestRedirectDoesNotForwardAuthorization(t *testing.T) {
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		if r.Header.Get("Authorization") != "" {
			t.Error("authorization header followed redirect")
		}
	}))
	defer target.Close()
	api := newFakeAPI()
	api.redirectTo = target.URL
	g, _ := newTestGate(t, api)
	g.token = "secret-test-token"
	_, err := g.wait(context.Background())
	if err == nil || strings.Contains(err.Error(), g.token) || targetCalls != 0 {
		t.Fatalf("err=%v target calls=%d; redirect must fail without forwarding credentials", err, targetCalls)
	}
	if !api.authSeen {
		t.Fatal("configured credential was not sent to the configured API origin")
	}
}

func TestAPIControlledFailureTextCannotEchoCredential(t *testing.T) {
	api := newFakeAPI()
	candidate := testRun(125, 1, "completed", "secret-test-token")
	api.runs = []run{candidate}
	api.details[125] = []run{candidate}
	g, _ := newTestGate(t, api)
	g.token = "secret-test-token"
	_, err := g.wait(context.Background())
	if err == nil || strings.Contains(err.Error(), g.token) {
		t.Fatalf("error leaked API-controlled credential text: %v", err)
	}
}

func TestProofRejectsUntrustedRunURL(t *testing.T) {
	for _, value := range []string{
		"http://github.com/owner/project/actions/runs/42",
		"https://github.com.evil.invalid/owner/project/actions/runs/42",
		"https://github.com/owner/project/actions/runs/42?token=secret",
		"https://user:pass@github.com/owner/project/actions/runs/42",
	} {
		if safeRunURL(value, "owner/project", 42) {
			t.Errorf("accepted unsafe run URL %q", value)
		}
	}
	if !safeRunURL("https://github.com/owner/project/actions/runs/42", "owner/project", 42) {
		t.Fatal("rejected expected GitHub run URL")
	}
}

func TestLatestRunAppearingDuringFinalRefreshSupersedesGreenProof(t *testing.T) {
	api := newFakeAPI()
	old := testRun(120, 1, "completed", "success")
	newer := testRun(121, 1, "completed", "success")
	api.runs = []run{old}
	api.newRunAfterListCall = 2
	api.newerRun = &newer
	api.details[120] = []run{old}
	api.details[121] = []run{newer}
	g, _ := newTestGate(t, api)
	proof, err := g.wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if proof.RunID != newer.ID {
		t.Fatalf("proof run=%d, want newly observed run %d", proof.RunID, newer.ID)
	}
}

func TestTransientHTTPFailureRetriesWithinDeadline(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := newFakeAPI()
			api.runs = []run{testRun(122, 1, "completed", "success")}
			api.details[122] = []run{api.runs[0]}
			api.retryStatus = status
			api.retryRemaining = 2
			g, clock := newTestGate(t, api)
			if _, err := g.wait(context.Background()); err != nil {
				t.Fatalf("wait after transient HTTP failures: %v", err)
			}
			if clock.sleeps < 2 {
				t.Fatalf("retry sleeps=%d, want bounded retry backoff", clock.sleeps)
			}
		})
	}
}

func TestPermissionHTTPFailuresDoNotRetry(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := newFakeAPI()
			api.failureStatus = status
			g, clock := newTestGate(t, api)
			if _, err := g.wait(context.Background()); err == nil {
				t.Fatal("accepted authorization/not-found failure")
			}
			if clock.sleeps != 0 {
				t.Fatalf("slept %d times for non-retryable HTTP %d", clock.sleeps, status)
			}
		})
	}
}

func TestNextPaginationLinkCannotChangeOrigin(t *testing.T) {
	current, _ := url.Parse("https://api.github.com/repos/owner/project/actions/runs?page=1&per_page=100")
	header := http.Header{"Link": {`<https://evil.invalid/steal?page=2&per_page=100>; rel="next"`}}
	if _, err := nextPage(header, current, current.Path, url.Values{"page": {"1"}, "per_page": {"100"}}); err == nil {
		t.Fatal("accepted cross-origin pagination link")
	}
}
func TestPaginationLinkCannotDropExactSHAFilter(t *testing.T) {
	current, _ := url.Parse("https://api.github.com/repos/owner/project/actions/runs?page=1&per_page=100&head_sha=" + testSHA)
	header := http.Header{"Link": {`<https://api.github.com/repos/owner/project/actions/runs?page=2&per_page=100>; rel="next"`}}
	query := url.Values{"page": {"1"}, "per_page": {"100"}, "head_sha": {testSHA}}
	if _, err := nextPage(header, current, current.Path, query); err == nil {
		t.Fatal("accepted pagination link that dropped head_sha filter")
	}
}

func TestCLIHelpIsSuccessful(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := runCLI([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code=%d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-repo") {
		t.Fatalf("help output omitted CLI flags: %q", stderr.String())
	}
}

func TestCLIRejectsInvalidArguments(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := runCLI([]string{"--repo", "owner/project", "--sha", "short"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code=%d, want 2; stderr=%s", code, stderr.String())
	}
}

type fakeClock struct {
	mu      sync.Mutex
	current time.Time
	elapsed time.Duration
	sleeps  int
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *fakeClock) sleep(ctx context.Context, duration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.mu.Lock()
	c.current = c.current.Add(duration)
	c.elapsed += duration
	c.sleeps++
	c.mu.Unlock()
	return nil
}

type fakeAPI struct {
	workflow            workflow
	runs                []run
	runsPages           map[int]runList
	details             map[int64][]run
	detailCalls         map[int64]int
	jobMode             string
	failureMode         string
	failureStatus       int
	retryStatus         int
	retryRemaining      int
	newRunAfterListCall int
	newerRun            *run
	queryMismatch       bool
	paginateJobs        bool
	blockWorkflow       bool
	redirectTo          string
	authSeen            bool
	jobCalls            int
	jobAttempts         map[int]int
	jobPageCalls        map[int]int
	runListCalls        int
	runPageCalls        map[int]int
	server              *httptest.Server
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		workflow: workflow{ID: 9, Path: workflowPath},
		details:  map[int64][]run{}, detailCalls: map[int64]int{}, jobAttempts: map[int]int{},
		jobPageCalls: map[int]int{}, runPageCalls: map[int]int{},
	}
}

func newTestGate(t *testing.T, api *fakeAPI) (gate, *fakeClock) {
	t.Helper()
	api.server = httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	t.Cleanup(api.server.Close)
	clock := &fakeClock{current: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)}
	return gate{
		repo: "owner/project", sha: testSHA, client: api.server.Client(), baseURL: api.server.URL,
		now: clock.now, sleep: clock.sleep, timeout: time.Minute, pollEvery: time.Second, requestMax: time.Second,
	}, clock
}

func (api *fakeAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") == "Bearer secret-test-token" {
		api.authSeen = true
	}
	if api.redirectTo != "" {
		http.Redirect(w, r, api.redirectTo, http.StatusFound)
		return
	}
	if api.blockWorkflow && strings.HasSuffix(r.URL.Path, "/actions/workflows/ci.yml") {
		<-r.Context().Done()
		return
	}
	if api.failureMode == "http-500" {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if api.failureStatus != 0 {
		http.Error(w, "API refused fake request", api.failureStatus)
		return
	}
	if api.retryRemaining > 0 {
		api.retryRemaining--
		w.Header().Set("Retry-After", "0")
		http.Error(w, "temporarily unavailable", api.retryStatus)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/actions/workflows/ci.yml"):
		api.writeJSON(w, api.workflow)
	case strings.HasSuffix(r.URL.Path, "/actions/workflows/9/runs"):
		api.runListCalls++
		if r.URL.Query().Get("event") != event || r.URL.Query().Get("branch") != branch ||
			r.URL.Query().Get("head_sha") != testSHA || r.URL.Query().Get("per_page") != fmt.Sprint(pageSize) {
			api.queryMismatch = true
		}
		if api.newerRun != nil && api.runListCalls >= api.newRunAfterListCall {
			api.runs = append(api.runs, *api.newerRun)
		}
		page := queryPage(r)
		api.runPageCalls[page]++
		if api.failureMode == "incomplete-runs" {
			if page == 1 {
				w.Header().Set("Link", api.paginationLink(r))
				api.writeJSON(w, runList{TotalCount: 2, WorkflowRuns: api.runs})
			} else {
				api.writeJSON(w, runList{TotalCount: 2})
			}
			return
		}
		if api.runsPages != nil {
			if page == 1 {
				w.Header().Set("Link", api.paginationLink(r))
			}
			api.writeJSON(w, api.runsPages[page])
			return
		}
		api.writeJSON(w, runList{TotalCount: len(api.runs), WorkflowRuns: api.runs})
	case strings.Contains(r.URL.Path, "/actions/runs/") && strings.HasSuffix(r.URL.Path, "/jobs"):
		api.serveJobs(w, r)
	case strings.Contains(r.URL.Path, "/actions/runs/"):
		var id int64
		_, _ = fmt.Sscanf(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "%d", &id)
		api.detailCalls[id]++
		sequence := api.details[id]
		if len(sequence) == 0 {
			for _, candidate := range api.runs {
				if candidate.ID == id {
					sequence = []run{candidate}
				}
			}
		}
		index := api.detailCalls[id] - 1
		if index >= len(sequence) && len(sequence) > 0 {
			index = len(sequence) - 1
		}
		if len(sequence) == 0 {
			http.NotFound(w, r)
			return
		}
		api.writeJSON(w, sequence[index])
	default:
		http.NotFound(w, r)
	}
}

func (api *fakeAPI) serveJobs(w http.ResponseWriter, r *http.Request) {
	api.jobCalls++
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	var attempt int
	for i, part := range parts {
		if part == "attempts" && i+1 < len(parts) {
			_, _ = fmt.Sscanf(parts[i+1], "%d", &attempt)
		}
	}
	api.jobAttempts[attempt]++
	page := queryPage(r)
	api.jobPageCalls[attempt]++
	if api.failureMode == "incomplete-jobs" {
		if page == 1 {
			w.Header().Set("Link", api.paginationLink(r))
			api.writeJSON(w, jobList{TotalCount: 2, Jobs: successfulJobs()})
		} else {
			api.writeJSON(w, jobList{TotalCount: 2})
		}
		return
	}
	jobs := successfulJobs()
	if api.jobMode == "empty" {
		jobs = nil
	}
	if api.jobMode == "missing-integration-complete" {
		filtered := jobs[:0]
		for _, item := range jobs {
			if item.Name != "integration-complete" {
				filtered = append(filtered, item)
			}
		}
		jobs = filtered
	}
	if api.jobMode == "failed-integration" {
		for i := range jobs {
			if strings.HasPrefix(jobs[i].Name, "integration (") {
				jobs[i].Conclusion = "failure"
				break
			}
		}
	}
	if api.jobMode == "duplicate-matrix" {
		for i := range jobs {
			switch {
			case strings.HasPrefix(jobs[i].Name, "build-image ("):
				jobs[i].Name = "build-image (same)"
			case strings.HasPrefix(jobs[i].Name, "integration ("):
				jobs[i].Name = "integration (same)"
			}
		}
	}
	if api.paginateJobs {
		if page == 1 {
			w.Header().Set("Link", api.paginationLink(r))
			api.writeJSON(w, jobList{TotalCount: len(jobs), Jobs: jobs[:1]})
			return
		}
		api.writeJSON(w, jobList{TotalCount: len(jobs), Jobs: jobs[1:]})
		return
	}
	api.writeJSON(w, jobList{TotalCount: len(jobs), Jobs: jobs})
}

func (api *fakeAPI) paginationLink(r *http.Request) string {
	query := r.URL.Query()
	query.Set("page", "2")
	return fmt.Sprintf(`<%s%s?%s>; rel="next"`, api.server.URL, r.URL.Path, query.Encode())
}

func (api *fakeAPI) writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(err)
	}
}

func queryPage(r *http.Request) int {
	var page int
	_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
	if page < 1 {
		return 1
	}
	return page
}

func testRun(id int64, attempt int, status, conclusion string) run {
	return run{
		ID: id, RunNumber: id, RunAttempt: attempt, WorkflowID: 9, Path: workflowPath,
		HeadSHA: testSHA, HeadBranch: branch, Event: event, Status: status, Conclusion: conclusion,
		HTMLURL:    fmt.Sprintf("https://github.com/owner/project/actions/runs/%d", id),
		Repository: repository{FullName: "owner/project"}, HeadRepository: repository{FullName: "owner/project"},
	}
}

func successfulJobs() []job {
	return []job{
		{Name: "secrets-scan", Conclusion: "success"},
		{Name: "check-go", Conclusion: "success"},
		{Name: "check-macos", Conclusion: "success"},
		{Name: "build-image (ubuntu-latest)", Conclusion: "success"},
		{Name: "build-image (ubuntu-24.04-arm)", Conclusion: "success"},
		{Name: "integration (ubuntu-latest, shard 1)", Conclusion: "success"},
		{Name: "integration (ubuntu-latest, shard 2)", Conclusion: "success"},
		{Name: "integration (ubuntu-24.04-arm, shard 1)", Conclusion: "success"},
		{Name: "integration (ubuntu-24.04-arm, shard 2)", Conclusion: "success"},
		{Name: "integration-complete", Conclusion: "success"},
	}
}

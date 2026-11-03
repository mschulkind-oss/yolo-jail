package main

import (
	"bufio"
	"bytes"
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

// The darwin floor gate: before a release is tagged, both macOS floor profiles
// for the target commit must be in the project's Cachix cache, or every
// macos-user user of the release compiles the rustls mise on first launch. The
// caller computes the store paths from the target flake (a nix eval, no build)
// and the set of main commits whose flake inputs equal the target's; this code
// asks the cache and, while a darwin-floor-cache.yml run for one of those
// commits is still going, waits for it. When the cache misses with no such run
// pending, it dispatches one run that builds the exact target commit, waits for
// it, and passes only when that run succeeded and the cache then serves every
// path (docs/reference/release-process.md#PTG-D5). It dispatches at most once.

const (
	floorWorkflowFile = "darwin-floor-cache.yml"
	floorWorkflowPath = ".github/workflows/" + floorWorkflowFile
	floorRunPages     = 3
	maxWaitSHAs       = 5000
	maxNarinfoBytes   = 64 << 10
	// floorRunTitlePrefix is the start of the run-name darwin-floor-cache.yml
	// gives every run; the full SHA of the commit the run checks out and builds
	// follows it. The gate finds its own dispatched run by that title.
	floorRunTitlePrefix = "darwin floor cache for "
	// floorRunAppearMax bounds how long an accepted dispatch may take to show
	// up as a run before the gate gives up on it.
	floorRunAppearMax = 5 * time.Minute
)

var (
	storePathPattern  = regexp.MustCompile(`^/nix/store/([0-9a-df-np-sv-z]{32})-[A-Za-z0-9+._?=-]+$`)
	cacheNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	floorTitlePattern = regexp.MustCompile(`^` + floorRunTitlePrefix + `([0-9a-f]{40})$`)
)

type floorGate struct {
	api      gate // Actions API access: repo, token, baseURL, client, clock
	cache    string
	cacheURL string
	// upstreamURLs are binary caches every user's nix already substitutes from
	// (cache.nixos.org): a profile's reference one of them serves needs no copy
	// in the project's cache, and `cachix push` skips exactly those paths, so
	// requiring them there would refuse every release whose floor reuses
	// nixpkgs' own builds. The profiles themselves must be in the project's cache.
	upstreamURLs []string
	paths        []string
	waitSHAs     map[string]bool
	// targetSHA is the commit being released; a dispatched run builds exactly it.
	targetSHA string
	// progress receives one line per wait, so a log shows which run it is on.
	progress io.Writer
}

type floorProof struct {
	Cache string   `json:"cache"`
	Paths []string `json:"paths"`
	// RunURL is the darwin-floor-cache.yml run this gate dispatched, when it
	// had to dispatch one.
	RunURL string `json:"run_url,omitempty"`
}

// floorRefusal is a final answer the release request must stop on; its text
// names what to check and the retry.
type floorRefusal struct{ msg string }

func (r floorRefusal) Error() string { return r.msg }

// ownDispatch is the one run this gate dispatched. before holds the IDs of the
// runs already titled for the target when it dispatched, so an older run with
// the same title (an earlier request's) is never taken for it.
type ownDispatch struct {
	before map[int64]bool
	at     time.Time
	run    *run
}

func (f *floorGate) ownTitle() string { return floorRunTitlePrefix + f.targetSHA }

func (f *floorGate) dispatchCommand() string {
	return fmt.Sprintf("gh workflow run %s --repo %s --ref %s -f sha=%s", floorWorkflowFile, f.api.repo, branch, f.targetSHA)
}

// retryStep ends every refusal: what to check, and the retry.
func (f *floorGate) retryStep(r *run) string {
	check := "the log of the newest " + floorWorkflowFile + " run"
	if r != nil {
		check = fmt.Sprintf("the run's log (gh run view %d --repo %s --log-failed)", r.ID, f.api.repo)
	}
	return fmt.Sprintf("Check %s, that the CACHIX_AUTH_TOKEN repository secret is set and can push to https://%s.cachix.org, "+
		"and that the macOS runners (macos-latest, macos-26-intel) are available; then request this release again, "+
		"which builds the floor again (no tag was created, so the version is still unreserved)", check, f.cache)
}

func (f *floorGate) wait(parent context.Context) (floorProof, error) {
	g := f.api
	if g.client == nil || g.now == nil || g.sleep == nil || g.timeout <= 0 || g.pollEvery <= 0 || g.requestMax <= 0 {
		return floorProof{}, errors.New("darwin floor gate is missing valid runtime configuration")
	}
	if len(f.paths) == 0 {
		return floorProof{}, errors.New("darwin floor gate was given no store paths")
	}
	if !shaPattern.MatchString(f.targetSHA) || strings.ToLower(f.targetSHA) != f.targetSHA {
		return floorProof{}, errors.New("darwin floor gate was given no valid lowercase target commit")
	}
	ctx, cancel := context.WithTimeout(parent, g.timeout)
	defer cancel()
	deadline := g.now().Add(g.timeout)
	g.deadline = deadline
	g.client = safeHTTPClient(g.client)

	var watched *run
	var own *ownDispatch
	// fail reports err, or the timeout refusal once the deadline has passed.
	fail := func(err error) (floorProof, error) {
		if g.checkDeadline(ctx, deadline) != nil {
			return floorProof{}, f.timedOut(watched, own)
		}
		return floorProof{}, err
	}
	for {
		if err := g.checkDeadline(ctx, deadline); err != nil {
			return floorProof{}, f.timedOut(watched, own)
		}
		runs, err := f.listRuns(ctx, &g)
		if err != nil {
			return fail(err)
		}
		if own == nil {
			pending, latest := f.relevantRuns(runs)
			missing, err := f.missingPaths(ctx, &g)
			if err != nil {
				return fail(err)
			}
			if pending == nil && len(missing) > 0 {
				// The run may have finished its push between the two reads above:
				// read the cache once more before dispatching.
				if missing, err = f.missingPaths(ctx, &g); err != nil {
					return fail(err)
				}
			}
			if pending == nil && len(missing) == 0 {
				return floorProof{Cache: f.cache, Paths: append([]string(nil), f.paths...)}, nil
			}
			if pending == nil {
				// PTG-D5: build it, and require the build to pass. One dispatch,
				// for the exact target; never a second.
				before := map[int64]bool{}
				for _, r := range runs {
					if r.DisplayTitle == f.ownTitle() {
						before[r.ID] = true
					}
				}
				if err := f.dispatch(ctx, &g); err != nil {
					return fail(err)
				}
				own = &ownDispatch{before: before, at: g.now()}
				if f.progress != nil {
					note := ""
					if latest != nil {
						note = fmt.Sprintf(" (the newest relevant one, %s, concluded %q)", latest.HTMLURL, latest.Conclusion)
					}
					fmt.Fprintf(f.progress, "%s are not in https://%s.cachix.org and no relevant %s run is pending%s; dispatched one to build %s: %s\n",
						strings.Join(missing, ", "), f.cache, floorWorkflowFile, note, f.targetSHA, f.dispatchCommand())
				}
			} else {
				watched = pending
				if f.progress != nil {
					if len(missing) == 0 {
						fmt.Fprintf(f.progress, "The floor profiles are cached; waiting for %s run %d (%s) to finish its push\n", floorWorkflowFile, pending.ID, pending.Status)
					} else {
						fmt.Fprintf(f.progress, "Waiting for %s run %d (%s) to push %s\n", floorWorkflowFile, pending.ID, pending.Status, strings.Join(missing, ", "))
					}
				}
			}
		} else {
			if r := f.findOwnRun(runs, own); r != nil {
				own.run, watched = r, r
			}
			switch {
			case own.run == nil:
				if g.now().Sub(own.at) >= floorRunAppearMax {
					return floorProof{}, floorRefusal{fmt.Sprintf("GitHub accepted the %s dispatch for %s, but no run titled %q appeared within %s. %s",
						floorWorkflowFile, f.targetSHA, f.ownTitle(), floorRunAppearMax, f.retryStep(nil))}
				}
				if f.progress != nil {
					fmt.Fprintf(f.progress, "Waiting for the dispatched %s run for %s to appear\n", floorWorkflowFile, f.targetSHA)
				}
			case own.run.Status == "completed":
				r := own.run
				if r.Conclusion != "success" {
					return floorProof{}, floorRefusal{fmt.Sprintf("the %s run this request dispatched to build %s, %s, concluded %q, so the darwin floor profiles are not in https://%s.cachix.org. %s",
						floorWorkflowFile, f.targetSHA, r.HTMLURL, r.Conclusion, f.cache, f.retryStep(r))}
				}
				missing, err := f.missingPaths(ctx, &g)
				if err != nil {
					return fail(err)
				}
				if len(missing) > 0 {
					return floorProof{}, floorRefusal{fmt.Sprintf("the %s run this request dispatched to build %s, %s, succeeded, but https://%s.cachix.org still does not serve %s. %s",
						floorWorkflowFile, f.targetSHA, r.HTMLURL, f.cache, strings.Join(missing, ", "), f.retryStep(r))}
				}
				return floorProof{Cache: f.cache, Paths: append([]string(nil), f.paths...), RunURL: r.HTMLURL}, nil
			default:
				if f.progress != nil {
					fmt.Fprintf(f.progress, "Waiting for the dispatched %s run %d (%s) to build and push the floor of %s\n", floorWorkflowFile, own.run.ID, own.run.Status, f.targetSHA)
				}
			}
		}
		remaining := deadline.Sub(g.now())
		pause := g.pollEvery
		if remaining < pause {
			pause = remaining
		}
		if pause <= 0 {
			return floorProof{}, f.timedOut(watched, own)
		}
		if err := g.sleep(ctx, pause); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return floorProof{}, f.timedOut(watched, own)
			}
			return floorProof{}, fmt.Errorf("waiting for the darwin floor cache was interrupted: %w", err)
		}
	}
}

func (f *floorGate) timedOut(watched *run, own *ownDispatch) error {
	switch {
	case own != nil && own.run != nil:
		return floorRefusal{fmt.Sprintf("timed out waiting for the %s run this request dispatched to build %s, %s, to push the darwin floor profiles. %s",
			floorWorkflowFile, f.targetSHA, own.run.HTMLURL, f.retryStep(own.run))}
	case own != nil:
		return floorRefusal{fmt.Sprintf("timed out waiting for the %s run this request dispatched to build %s to appear. %s", floorWorkflowFile, f.targetSHA, f.retryStep(nil))}
	case watched != nil:
		return floorRefusal{fmt.Sprintf("timed out waiting for %s run %d (%s) to push the darwin floor profiles to https://%s.cachix.org. %s",
			floorWorkflowFile, watched.ID, watched.HTMLURL, f.cache, f.retryStep(watched))}
	}
	return floorRefusal{fmt.Sprintf("timed out reading https://%s.cachix.org for the darwin floor profiles. %s", f.cache, f.retryStep(nil))}
}

// dispatch asks GitHub to run main's darwin-floor-cache.yml for the exact
// target commit. The release-request job's GITHUB_TOKEN can start it: events
// that token causes start no workflow run, except workflow_dispatch and
// repository_dispatch, which always do
// (https://docs.github.com/en/actions/concepts/security/github_token#when-github_token-triggers-workflow-runs).
// It is never retried: a retried POST could start a second build.
func (f *floorGate) dispatch(ctx context.Context, g *gate) error {
	if err := g.checkDeadline(ctx, g.deadline); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"ref": branch, "inputs": map[string]string{"sha": f.targetSHA}})
	if err != nil {
		return errors.New("could not encode the darwin floor dispatch")
	}
	endpoint := strings.TrimRight(g.baseURL, "/") + "/repos/" + g.repo + "/actions/workflows/" + floorWorkflowFile + "/dispatches"
	requestContext, cancel := context.WithTimeout(ctx, g.requestMax)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("could not construct the darwin floor dispatch request")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	response, err := g.client.Do(req)
	if err != nil {
		return floorRefusal{fmt.Sprintf("the %s dispatch to build %s did not reach the Actions API (it is not retried, so as not to start a second build). %s",
			floorWorkflowFile, f.targetSHA, f.retryStep(nil))}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return floorRefusal{fmt.Sprintf("GitHub refused the %s dispatch to build %s (HTTP %d). Check that the release-request job's token has actions: write and that main's %s has the sha input (the dispatch was the equivalent of %s). %s",
			floorWorkflowFile, f.targetSHA, response.StatusCode, floorWorkflowFile, f.dispatchCommand(), f.retryStep(nil))}
	}
	return nil
}

// missingPaths returns the store paths the cache does not serve: each profile
// whose narinfo is absent, and, for a profile that is present, each path its
// narinfo's References line names that neither the project's cache nor an
// upstream cache serves (the floor's own packages, mise among them). Each path
// is asked once per call.
func (f *floorGate) missingPaths(ctx context.Context, g *gate) ([]string, error) {
	var missing []string
	seen := map[string]bool{}
	check := func(path string, profile bool) ([]string, error) {
		if seen[path] {
			return nil, nil
		}
		seen[path] = true
		hit, refs, err := f.narinfoHit(ctx, g, f.cacheURL, path)
		if err != nil {
			return nil, err
		}
		if !hit && !profile {
			for _, upstream := range f.upstreamURLs {
				if hit, _, err = f.narinfoHit(ctx, g, upstream, path); err != nil {
					return nil, err
				}
				if hit {
					break
				}
			}
		}
		if !hit {
			missing = append(missing, path)
		}
		return refs, nil
	}
	for _, path := range f.paths {
		refs, err := check(path, true)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			if _, err := check(ref, false); err != nil {
				return nil, err
			}
		}
	}
	return missing, nil
}

// narinfoHit reports whether the cache at base serves path's narinfo and, when
// it does, the other store paths that narinfo's References line names.
func (f *floorGate) narinfoHit(ctx context.Context, g *gate, base, path string) (bool, []string, error) {
	m := storePathPattern.FindStringSubmatch(path)
	if m == nil {
		return false, nil, fmt.Errorf("invalid store path %q", path)
	}
	endpoint := strings.TrimRight(base, "/") + "/" + m[1] + ".narinfo"
	for attempt := 0; attempt <= 3; attempt++ {
		if err := g.checkDeadline(ctx, g.deadline); err != nil {
			return false, nil, err
		}
		requestContext, cancel := context.WithTimeout(ctx, g.requestMax)
		req, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			return false, nil, errors.New("could not construct the Cachix narinfo request")
		}
		response, err := g.client.Do(req)
		if err != nil {
			cancel()
			return false, nil, fmt.Errorf("the Cachix narinfo request for %s failed; check https://%s.cachix.org is reachable and request again", path, f.cache)
		}
		switch {
		case response.StatusCode == http.StatusNotFound:
			response.Body.Close()
			cancel()
			return false, nil, nil
		case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
			delay := retryDelay(response.Header.Get("Retry-After"), attempt, g.now())
			response.Body.Close()
			cancel()
			if attempt == 3 {
				return false, nil, fmt.Errorf("the Cachix cache remained unavailable (HTTP %d) for %s; request this release again once https://%s.cachix.org answers", response.StatusCode, path, f.cache)
			}
			if remaining := g.deadline.Sub(g.now()); delay > remaining {
				delay = remaining
			}
			if err := g.sleep(ctx, delay); err != nil {
				return false, nil, errors.New("the Cachix narinfo retry was interrupted")
			}
			continue
		case response.StatusCode != http.StatusOK:
			response.Body.Close()
			cancel()
			return false, nil, fmt.Errorf("the Cachix cache returned HTTP %d for %s; this gate reads only a public cache, so check https://%s.cachix.org is public and is the one the CACHIX_CACHE variable names, then request again", response.StatusCode, path, f.cache)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, maxNarinfoBytes+1))
		response.Body.Close()
		cancel()
		if err != nil || len(body) > maxNarinfoBytes {
			return false, nil, fmt.Errorf("could not read the Cachix narinfo for %s; request this release again", path)
		}
		if !narinfoNames(body, path) {
			return false, nil, fmt.Errorf("the Cachix narinfo for %s names a different store path; inspect https://%s.cachix.org before releasing", path, f.cache)
		}
		refs, err := narinfoReferences(body, path)
		if err != nil {
			return false, nil, fmt.Errorf("the Cachix narinfo for %s %s; inspect https://%s.cachix.org before releasing", path, err, f.cache)
		}
		return true, refs, nil
	}
	return false, nil, errors.New("the Cachix narinfo retry limit was reached")
}

// narinfoReferences returns the store paths a narinfo's References line names,
// without path itself. Each entry is a store path's base name.
func narinfoReferences(body []byte, path string) ([]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok || key != "References" {
			continue
		}
		var refs []string
		for _, name := range strings.Fields(value) {
			ref := "/nix/store/" + name
			if !storePathPattern.MatchString(ref) {
				return nil, fmt.Errorf("names an invalid reference %q", name)
			}
			if ref != path {
				refs = append(refs, ref)
			}
		}
		return refs, nil
	}
	return nil, nil
}

func narinfoNames(body []byte, path string) bool {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		if key, value, ok := strings.Cut(scanner.Text(), ":"); ok && key == "StorePath" {
			return strings.TrimSpace(value) == path
		}
	}
	return false
}

// listRuns returns the newest darwin-floor-cache.yml runs of this repository's
// own workflow. A workflow the default branch does not have yet has no runs.
func (f *floorGate) listRuns(ctx context.Context, g *gate) ([]run, error) {
	var wf workflow
	if _, err := g.getJSON(ctx, "/repos/"+g.repo+"/actions/workflows/"+floorWorkflowFile, nil, &wf); err != nil {
		var status statusError
		if errors.As(err, &status) && status.code == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	if wf.ID <= 0 || wf.Path != floorWorkflowPath {
		return nil, errors.New("the Actions workflow identity did not match " + floorWorkflowPath)
	}
	endpoint := fmt.Sprintf("/repos/%s/actions/workflows/%d/runs", g.repo, wf.ID)
	query := url.Values{"per_page": {strconv.Itoa(pageSize)}}
	var out []run
	for page := 1; page <= floorRunPages; page++ {
		query.Set("page", strconv.Itoa(page))
		var result runList
		next, err := g.getJSON(ctx, endpoint, query, &result)
		if err != nil {
			return nil, err
		}
		for _, candidate := range result.WorkflowRuns {
			if f.ours(candidate, wf.ID) {
				out = append(out, candidate)
			}
		}
		if next == nil {
			break
		}
		query = next
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RunNumber == out[j].RunNumber {
			return out[i].ID > out[j].ID
		}
		return out[i].RunNumber > out[j].RunNumber
	})
	return out, nil
}

// ours: a run of this repository's own darwin-floor-cache.yml, from this
// repository rather than a fork.
func (f *floorGate) ours(candidate run, workflowID int64) bool {
	return candidate.ID > 0 && candidate.WorkflowID == workflowID &&
		(candidate.Path == floorWorkflowPath || strings.HasPrefix(candidate.Path, floorWorkflowPath+"@")) &&
		candidate.Repository.FullName == f.api.repo && candidate.HeadRepository.FullName == f.api.repo
}

func pendingStatus(status string) bool {
	switch status {
	case "queued", "in_progress", "waiting", "pending", "requested":
		return true
	}
	return false
}

// relevantRuns returns, of runs (newest first), the newest still-pending
// relevant run and the newest relevant run of any status.
func (f *floorGate) relevantRuns(runs []run) (pending, latest *run) {
	for i := range runs {
		if !f.relevant(runs[i]) {
			continue
		}
		if latest == nil {
			latest = &runs[i]
		}
		if pendingStatus(runs[i].Status) {
			return &runs[i], latest
		}
	}
	return nil, latest
}

// builtSHA is the commit a run checks out and builds. A run titled by the
// workflow's run-name says it: a dispatch with a sha input builds that commit,
// whatever main's tip was. A run from before the run-name existed builds its
// head_sha.
func builtSHA(candidate run) string {
	if m := floorTitlePattern.FindStringSubmatch(candidate.DisplayTitle); m != nil {
		return m[1]
	}
	return candidate.HeadSHA
}

// relevant: a run that builds one of the wait commits, so would push these
// exact paths. A push counts only on main; a dispatch on any branch (a run
// dispatched on a branch at the target, the step this gate named before it
// dispatched for itself, still builds the target).
func (f *floorGate) relevant(candidate run) bool {
	return ((candidate.Event == "push" && candidate.HeadBranch == branch) || (candidate.Event == "workflow_dispatch" && candidate.HeadBranch != "")) &&
		f.waitSHAs[builtSHA(candidate)]
}

// findOwnRun returns the run this gate's dispatch started: a workflow_dispatch
// run of main's workflow titled for the target that was not there before the
// dispatch. Should two appear (someone dispatched the same build by hand at the
// same moment), the older is taken; both build the same commit from the same
// workflow, and the cache read after it is the proof either way.
func (f *floorGate) findOwnRun(runs []run, own *ownDispatch) *run {
	var found *run
	for i := range runs {
		r := &runs[i]
		if r.Event != "workflow_dispatch" || r.HeadBranch != branch || r.DisplayTitle != f.ownTitle() || own.before[r.ID] {
			continue
		}
		if found == nil || r.ID < found.ID {
			found = r
		}
	}
	return found
}

// parseWaitSHAs reads one lowercase full commit SHA per line.
func parseWaitSHAs(r io.Reader) (map[string]bool, error) {
	out := map[string]bool{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !shaPattern.MatchString(line) {
			return nil, fmt.Errorf("invalid commit SHA %q in the wait list", line)
		}
		out[strings.ToLower(line)] = true
		if len(out) > maxWaitSHAs {
			return nil, fmt.Errorf("the wait list exceeds %d commits", maxWaitSHAs)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("the wait list is empty")
	}
	return out, nil
}

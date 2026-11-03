package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	floorArm   = "/nix/store/c4xkimsmafy3as7qyq6nli4qx3m4xwvp-yolo-noncontainer-profile"
	floorIntel = "/nix/store/4ir7jys56ds80ph8sn5kg749km8l0y9w-yolo-noncontainer-profile"
	flakeSHA   = "fedcba9876543210fedcba9876543210fedcba98"
	otherSHA   = "1111111111111111111111111111111111111111"
)

// fakeFloor stubs both the Cachix narinfo endpoint and the Actions API.
type fakeFloor struct {
	mu sync.Mutex
	// present maps a store path to the poll count (cache checks of that path)
	// after which the cache serves it; 0 means from the start, absent never.
	present       map[string]int
	checks        map[string]int
	narinfoPath   map[string]string // override the StorePath a narinfo names
	cacheFailures int               // 503s before the cache answers
	cacheStatus   int               // a fixed non-200/404 answer
	noWorkflow    bool
	runs          []run
	// runSeq, when set, answers the Nth run listing with runSeq[N], the last
	// repeating: a run GitHub reports changing while the gate polls.
	runSeq [][]run
	// refs is a narinfo's References line, as store path base names.
	refs map[string][]string
	// upstream holds the store paths the stub upstream cache (cache.nixos.org's
	// stand-in, under /upstream/) serves, and upstreamReads counts its reads.
	upstream      map[string]bool
	upstreamReads map[string]int
	runsCalls     int
	// dispatches records each workflow_dispatch body the gate POSTed;
	// dispatchStatus answers it (204 when zero), and afterDispatch, when set,
	// answers the Nth run listing after the dispatch with the usual runs plus
	// afterDispatch[N], the last repeating: the run the dispatch started.
	dispatches     []map[string]any
	dispatchStatus int
	afterDispatch  [][]run
	listsSince     int
	server         *httptest.Server
}

func newFakeFloor(t *testing.T) *fakeFloor {
	t.Helper()
	f := &fakeFloor{present: map[string]int{}, checks: map[string]int{}, narinfoPath: map[string]string{}, refs: map[string][]string{},
		upstream: map[string]bool{}, upstreamReads: map[string]int{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeFloor) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/upstream/") && strings.HasSuffix(r.URL.Path, ".narinfo"):
		hash := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/upstream/"), ".narinfo")
		for path := range f.upstream {
			if strings.Contains(path, "/"+hash+"-") {
				f.upstreamReads[path]++
				fmt.Fprintf(w, "StorePath: %s\nURL: nar/x.nar.xz\nCompression: xz\nReferences: \n", path)
				return
			}
		}
		http.NotFound(w, r)
	case strings.HasPrefix(r.URL.Path, "/cache/") && strings.HasSuffix(r.URL.Path, ".narinfo"):
		if f.cacheFailures > 0 {
			f.cacheFailures--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if f.cacheStatus != 0 {
			w.WriteHeader(f.cacheStatus)
			return
		}
		hash := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/cache/"), ".narinfo")
		for path, after := range f.present {
			if !strings.Contains(path, "/"+hash+"-") {
				continue
			}
			f.checks[path]++
			if f.checks[path] > after {
				named := path
				if override, ok := f.narinfoPath[path]; ok {
					named = override
				}
				fmt.Fprintf(w, "StorePath: %s\nURL: nar/x.nar.zst\nCompression: zstd\nReferences: %s\n", named, strings.Join(f.refs[path], " "))
				return
			}
		}
		http.NotFound(w, r)
	case r.URL.Path == "/repos/owner/project/actions/workflows/"+floorWorkflowFile+"/dispatches":
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer write-token" {
			http.Error(w, "bad dispatch", http.StatusBadRequest)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.dispatches = append(f.dispatches, body)
		if f.dispatchStatus != 0 {
			w.WriteHeader(f.dispatchStatus)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/repos/owner/project/actions/workflows/"+floorWorkflowFile:
		if f.noWorkflow {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(workflow{ID: 77, Path: floorWorkflowPath})
	case r.URL.Path == "/repos/owner/project/actions/workflows/77/runs":
		runs := f.runs
		if len(f.runSeq) > 0 {
			runs = f.runSeq[min(f.runsCalls, len(f.runSeq)-1)]
		}
		f.runsCalls++
		if len(f.dispatches) > 0 && len(f.afterDispatch) > 0 {
			runs = append(append([]run(nil), runs...), f.afterDispatch[min(f.listsSince, len(f.afterDispatch)-1)]...)
			f.listsSince++
		}
		_ = json.NewEncoder(w).Encode(runList{TotalCount: len(runs), WorkflowRuns: runs})
	default:
		http.Error(w, "unexpected "+r.URL.Path, http.StatusTeapot)
	}
}

func floorRun(id int64, sha, status, conclusion string) run {
	return run{
		ID: id, RunNumber: id, RunAttempt: 1, WorkflowID: 77, Path: floorWorkflowPath, HeadSHA: sha,
		HeadBranch: "main", Event: "push", Status: status, Conclusion: conclusion,
		HTMLURL:    fmt.Sprintf("https://github.com/owner/project/actions/runs/%d", id),
		Repository: repository{FullName: "owner/project"}, HeadRepository: repository{FullName: "owner/project"},
	}
}

// dispatchedRun is the run darwin-floor-cache.yml starts for a dispatch from
// main with sha=testSHA: its head is main's tip, its title names the target.
func dispatchedRun(id int64, status, conclusion string) run {
	r := floorRun(id, otherSHA, status, conclusion)
	r.Event, r.DisplayTitle = "workflow_dispatch", floorRunTitlePrefix+testSHA
	return r
}

func (f *fakeFloor) dispatchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dispatches)
}

func newTestFloorGate(t *testing.T, f *fakeFloor) (*floorGate, *fakeClock) {
	t.Helper()
	clock := &fakeClock{current: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	return &floorGate{
		api: gate{
			repo: "owner/project", token: "write-token", client: f.server.Client(), baseURL: f.server.URL,
			now: clock.now, sleep: clock.sleep, timeout: 10 * time.Minute, pollEvery: 30 * time.Second, requestMax: time.Second,
		},
		cache: "yolo-jail", cacheURL: f.server.URL + "/cache", upstreamURLs: []string{f.server.URL + "/upstream"},
		paths:    []string{floorArm, floorIntel},
		waitSHAs: map[string]bool{testSHA: true, flakeSHA: true}, targetSHA: testSHA,
	}, clock
}

func TestFloorGatePassesWhenBothProfilesAreCached(t *testing.T) {
	f := newFakeFloor(t)
	f.present[floorArm], f.present[floorIntel] = 0, 0
	g, clock := newTestFloorGate(t, f)
	proof, err := g.wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if proof.Cache != "yolo-jail" || len(proof.Paths) != 2 || clock.sleeps != 0 {
		t.Fatalf("proof=%+v sleeps=%d; want an immediate pass", proof, clock.sleeps)
	}
}

func TestFloorGateWaitsForAPendingRunForTheFlakeCommit(t *testing.T) {
	for _, sha := range []string{testSHA, flakeSHA} {
		t.Run(sha[:7], func(t *testing.T) {
			f := newFakeFloor(t)
			f.present[floorArm] = 0
			f.present[floorIntel] = 3 // pushed after three checks
			running := []run{floorRun(5, sha, "in_progress", "")}
			f.runSeq = [][]run{running, running, running, {floorRun(5, sha, "completed", "success")}}
			g, clock := newTestFloorGate(t, f)
			if _, err := g.wait(context.Background()); err != nil {
				t.Fatalf("wait: %v", err)
			}
			if clock.sleeps != 3 || f.dispatchCount() != 0 {
				t.Fatalf("slept %d times and dispatched %d, want 3 polls while the run pushed and no dispatch", clock.sleeps, f.dispatchCount())
			}
		})
	}
}

// A miss with no relevant run pending is built, not refused (PTG-D5): the gate
// dispatches main's workflow once with the exact target SHA, waits for that
// run, and passes once it succeeded and the cache serves every path. Driven
// through the subcommand's entry point.
func TestFloorGateDispatchesTheTargetOnAMissAndPassesWhenItsRunPushes(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*fakeFloor)
	}{
		{"no runs at all", func(*fakeFloor) {}},
		{"a pending run for another flake", func(f *fakeFloor) { f.runs = []run{floorRun(6, otherSHA, "in_progress", "")} }},
		{"the relevant run failed", func(f *fakeFloor) { f.runs = []run{floorRun(7, flakeSHA, "completed", "failure")} }},
		{"a fork's run with the same SHA", func(f *fakeFloor) {
			r := floorRun(8, testSHA, "in_progress", "")
			r.HeadRepository.FullName = "fork/project"
			f.runs = []run{r}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFloor(t)
			f.present[floorArm] = 0
			f.present[floorIntel] = 2 // missed by the read and the re-read, served after the run
			tc.set(f)
			f.afterDispatch = [][]run{nil, {dispatchedRun(40, "queued", "")}, {dispatchedRun(40, "in_progress", "")}, {dispatchedRun(40, "completed", "success")}}
			code, stdout, stderr := runFloorAgainst(t, f)
			var proof floorProof
			if code != 0 || json.Unmarshal([]byte(stdout), &proof) != nil || proof.RunURL != "https://github.com/owner/project/actions/runs/40" || len(proof.Paths) != 2 {
				t.Fatalf("code=%d stdout=%q stderr=%q; want a pass proved by the dispatched run", code, stdout, stderr)
			}
			if len(f.dispatches) != 1 {
				t.Fatalf("dispatched %d times, want once", len(f.dispatches))
			}
			body := f.dispatches[0]
			inputs, _ := body["inputs"].(map[string]any)
			if body["ref"] != "main" || inputs["sha"] != testSHA || len(inputs) != 1 {
				t.Fatalf("dispatch body = %v, want ref main and sha %s", body, testSHA)
			}
			if !strings.Contains(stderr, "dispatched one to build "+testSHA) {
				t.Fatalf("stderr does not say it dispatched: %s", stderr)
			}
		})
	}
}

// runFloorAgainst drives `release-gate darwin-floor` against f with a fake
// clock, so its polls cost no time.
func runFloorAgainst(t *testing.T, f *fakeFloor) (int, string, string) {
	t.Helper()
	t.Setenv("GH_TOKEN", "write-token")
	var stdout, stderr bytes.Buffer
	clock := &fakeClock{current: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	code := runFloorCLI(floorCLIArgs(t, "--timeout", "30m", "--poll-interval", "30s"), &stdout, &stderr, func(g *floorGate) {
		g.api.client, g.api.baseURL, g.cacheURL = f.server.Client(), f.server.URL, f.server.URL+"/cache"
		g.api.now, g.api.sleep = clock.now, clock.sleep
	})
	return code, stdout.String(), stderr.String()
}

// Every way the build can fail to land in the cache fails the request: a run
// that fails or is cancelled, a run that succeeds while the cache still misses,
// a refused dispatch, a dispatch whose run never appears, and a run that does
// not finish in time. Each names what to check and the retry.
func TestFloorGateFailsTheRequestWhenItsBuildDoesNotLand(t *testing.T) {
	const runURL = "https://github.com/owner/project/actions/runs/40"
	for _, tc := range []struct {
		name string
		set  func(*fakeFloor)
		want []string
	}{
		{"the dispatched run fails", func(f *fakeFloor) {
			f.afterDispatch = [][]run{{dispatchedRun(40, "in_progress", "")}, {dispatchedRun(40, "completed", "failure")}}
		}, []string{runURL, `concluded "failure"`, "gh run view 40 --repo owner/project --log-failed"}},
		{"the dispatched run is cancelled", func(f *fakeFloor) {
			f.afterDispatch = [][]run{{dispatchedRun(40, "completed", "cancelled")}}
		}, []string{runURL, `concluded "cancelled"`}},
		{"the run succeeds but the cache still misses", func(f *fakeFloor) {
			f.afterDispatch = [][]run{{dispatchedRun(40, "completed", "success")}}
		}, []string{runURL, "succeeded, but https://yolo-jail.cachix.org still does not serve " + floorIntel}},
		{"the dispatch is refused", func(f *fakeFloor) { f.dispatchStatus = http.StatusUnprocessableEntity }, []string{
			"GitHub refused the darwin-floor-cache.yml dispatch", "HTTP 422", "actions: write",
			"gh workflow run darwin-floor-cache.yml --repo owner/project --ref main -f sha=" + testSHA,
		}},
		{"no run appears", func(*fakeFloor) {}, []string{"no run titled \"darwin floor cache for " + testSHA + "\" appeared"}},
		{"the run never finishes", func(f *fakeFloor) {
			f.afterDispatch = [][]run{{dispatchedRun(40, "in_progress", "")}}
		}, []string{"timed out", runURL}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFloor(t)
			f.present[floorArm] = 0
			tc.set(f)
			code, stdout, stderr := runFloorAgainst(t, f)
			if code != 1 || stdout != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q; want a failure and no proof", code, stdout, stderr)
			}
			for _, want := range append(tc.want, "CACHIX_AUTH_TOKEN", "macOS runners", "request this release again", "no tag was created") {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr lacks %q:\n%s", want, stderr)
				}
			}
			if len(f.dispatches) != 1 {
				t.Fatalf("dispatched %d times, want exactly once", len(f.dispatches))
			}
		})
	}
}

// An earlier run titled for the same target (a previous request's failed
// build) is not the run this dispatch started.
func TestFloorGateDoesNotTakeAnEarlierSameTitledRunForItsOwn(t *testing.T) {
	f := newFakeFloor(t)
	f.present[floorArm] = 0
	f.present[floorIntel] = 2
	f.runs = []run{dispatchedRun(30, "completed", "failure")}
	f.afterDispatch = [][]run{nil, {dispatchedRun(41, "completed", "success")}}
	code, stdout, stderr := runFloorAgainst(t, f)
	if code != 0 || !strings.Contains(stdout, "/actions/runs/41") {
		t.Fatalf("code=%d stdout=%q stderr=%q; want the new run 41 to prove it, not the old run 30", code, stdout, stderr)
	}
}

// A dispatch from main builds the commit its sha input names, so its run is
// relevant by its title, not by its head (main's tip).
func TestFloorGateWaitsForADispatchedRunTitledForTheTarget(t *testing.T) {
	f := newFakeFloor(t)
	f.present[floorArm], f.present[floorIntel] = 0, 2
	f.runSeq = [][]run{{dispatchedRun(5, "in_progress", "")}, {dispatchedRun(5, "completed", "success")}}
	g, clock := newTestFloorGate(t, f)
	g.waitSHAs = map[string]bool{testSHA: true}
	if _, err := g.wait(context.Background()); err != nil || clock.sleeps != 1 || f.dispatchCount() != 0 {
		t.Fatalf("err=%v sleeps=%d dispatches=%d; want one wait on the titled run and no dispatch", err, clock.sleeps, f.dispatchCount())
	}
}

func TestFloorGateTimesOutOnARunThatNeverPushes(t *testing.T) {
	f := newFakeFloor(t)
	f.runs = []run{floorRun(9, testSHA, "queued", "")}
	g, _ := newTestFloorGate(t, f)
	_, err := g.wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "gh run view 9 --repo owner/project") {
		t.Fatalf("err = %v, want a timeout naming the run to inspect", err)
	}
}

func TestFloorGateRejectsANarinfoForAnotherPath(t *testing.T) {
	f := newFakeFloor(t)
	f.present[floorArm], f.present[floorIntel] = 0, 0
	f.narinfoPath[floorIntel] = "/nix/store/4ir7jys56ds80ph8sn5kg749km8l0y9w-something-else"
	g, _ := newTestFloorGate(t, f)
	if _, err := g.wait(context.Background()); err == nil || !strings.Contains(err.Error(), "names a different store path") {
		t.Fatalf("err = %v, want a mismatched narinfo refused", err)
	}
}

func TestFloorGateRetriesATransientCacheFailure(t *testing.T) {
	f := newFakeFloor(t)
	f.present[floorArm], f.present[floorIntel] = 0, 0
	f.cacheFailures = 2
	g, _ := newTestFloorGate(t, f)
	if _, err := g.wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func TestFloorGateNamesAPrivateCacheAnswer(t *testing.T) {
	f := newFakeFloor(t)
	f.cacheStatus = http.StatusUnauthorized
	g, _ := newTestFloorGate(t, f)
	if _, err := g.wait(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "public") {
		t.Fatalf("err = %v, want the 401 explained", err)
	}
}

func TestParseWaitSHAsRefusesBadInput(t *testing.T) {
	for _, input := range []string{"", "\n\n", "not-a-sha\n", testSHA + "\n$(touch x)\n"} {
		if _, err := parseWaitSHAs(strings.NewReader(input)); err == nil {
			t.Errorf("parseWaitSHAs(%q) accepted bad input", input)
		}
	}
	got, err := parseWaitSHAs(strings.NewReader(testSHA + "\n\n" + strings.ToUpper(flakeSHA) + "\n"))
	if err != nil || !got[testSHA] || !got[flakeSHA] || len(got) != 2 {
		t.Fatalf("parseWaitSHAs = %v, %v", got, err)
	}
}

func floorCLIArgs(t *testing.T, extra ...string) []string {
	t.Helper()
	waitFile := filepath.Join(t.TempDir(), "wait")
	if err := os.WriteFile(waitFile, []byte(testSHA+"\n"+flakeSHA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return append([]string{"--repo", "owner/project", "--cache", "yolo-jail", "--path", floorArm, "--path", floorIntel,
		"--sha", testSHA, "--wait-shas", waitFile, "--timeout", "1m", "--poll-interval", "1s"}, extra...)
}

func TestFloorCLIRefusesInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--repo", "owner/project", "--cache", "yolo-jail", "--wait-shas", "x"},
		{"--repo", "owner/project", "--cache", "Bad Name", "--path", floorArm, "--wait-shas", "x"},
		{"--repo", "owner/project", "--cache", "yolo-jail", "--path", "/tmp/evil", "--wait-shas", "x"},
		{"--repo", "owner/project", "--cache", "yolo-jail", "--path", floorArm, "--wait-shas", "/nonexistent/list"},
	} {
		var stderr bytes.Buffer
		if code := runFloorCLI(args, &bytes.Buffer{}, &stderr, nil); code != 2 {
			t.Errorf("runFloorCLI(%q) = %d, want 2; stderr=%s", args, code, stderr.String())
		}
	}
}

func TestFloorCLIPrintsProofOnAHit(t *testing.T) {
	f := newFakeFloor(t)
	f.present[floorArm], f.present[floorIntel] = 0, 0
	var stdout, stderr bytes.Buffer
	code := runFloorCLI(floorCLIArgs(t), &stdout, &stderr, func(g *floorGate) {
		g.api.client, g.api.baseURL, g.cacheURL = f.server.Client(), f.server.URL, f.server.URL+"/cache"
	})
	var proof floorProof
	if code != 0 || json.Unmarshal(stdout.Bytes(), &proof) != nil || len(proof.Paths) != 2 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCLIDispatchesTheDarwinFloorSubcommand(t *testing.T) {
	var stderr bytes.Buffer
	if code := runCLI([]string{"darwin-floor", "--cache", "x"}, &bytes.Buffer{}, &stderr); code != 2 || !strings.Contains(stderr.String(), "release-gate darwin-floor") {
		t.Fatalf("code=%d stderr=%q; want the darwin-floor argument refusal", code, stderr.String())
	}
}

// A cache hit while a relevant run is still going is not a pass: the push may
// be part-way through, so the gate waits for the run to conclude.
func TestFloorGateWaitsOutAPendingRunEvenOnAHit(t *testing.T) {
	f := newFakeFloor(t)
	f.present[floorArm], f.present[floorIntel] = 0, 0
	running := []run{floorRun(5, testSHA, "in_progress", "")}
	f.runSeq = [][]run{running, running, {floorRun(5, testSHA, "completed", "success")}}
	g, clock := newTestFloorGate(t, f)
	if _, err := g.wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if clock.sleeps != 2 {
		t.Fatalf("slept %d times, want 2 polls until the run concluded", clock.sleeps)
	}
}

// With no run pending, a miss is read once more before the refusal, so a push
// that finished between the two reads passes.
func TestFloorGateRereadsTheCacheOnceBeforeRefusing(t *testing.T) {
	f := newFakeFloor(t)
	f.present[floorArm] = 0
	f.present[floorIntel] = 1 // served from the second read on
	f.runs = []run{floorRun(5, testSHA, "completed", "success")}
	g, clock := newTestFloorGate(t, f)
	if _, err := g.wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if clock.sleeps != 0 || f.checks[floorIntel] != 2 {
		t.Fatalf("sleeps=%d reads=%d; want one re-read and no wait", clock.sleeps, f.checks[floorIntel])
	}
}

// Every path a profile's narinfo references must be cached too: the rustls
// mise is one, and a profile without it would still build mise on a Mac.
func TestFloorGateRequiresEveryReferencedPath(t *testing.T) {
	const mise = "/nix/store/0123456789abcdfghijklmnpqrsvwxyz-mise-2026.10.0"
	for _, cached := range []bool{false, true} {
		f := newFakeFloor(t)
		f.present[floorArm], f.present[floorIntel] = 0, 0
		f.refs[floorArm] = []string{strings.TrimPrefix(floorArm, "/nix/store/"), strings.TrimPrefix(mise, "/nix/store/")}
		f.refs[floorIntel] = []string{strings.TrimPrefix(mise, "/nix/store/")}
		if cached {
			f.present[mise] = 0
		} else {
			// The miss is built, and the build "succeeds" without mise.
			f.afterDispatch = [][]run{{dispatchedRun(40, "completed", "success")}}
		}
		g, _ := newTestFloorGate(t, f)
		_, err := g.wait(context.Background())
		if cached {
			if err != nil {
				t.Fatalf("wait with every reference cached: %v", err)
			}
			if f.checks[mise] != 1 {
				t.Fatalf("mise was read %d times in one pass, want once", f.checks[mise])
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), mise) || strings.Contains(err.Error(), floorArm+",") {
			t.Fatalf("err = %v, want a refusal naming only the missing reference", err)
		}
	}
}

// A reference cache.nixos.org already serves passes without a copy in the
// project's cache, because `cachix push` skips such paths (the v0.16.0 request
// refused a floor whose only "missing" references were nixpkgs' own builds). A
// profile itself still must be in the project's cache, and a reference neither
// cache serves (the floor's own mise) is still refused.
func TestFloorGateAcceptsAReferenceTheUpstreamCacheServes(t *testing.T) {
	const openssl = "/nix/store/0568rp7whq8ysn4k2a7jranag23grgqn-openssl-3.5.9-dev"
	const mise = "/nix/store/0123456789abcdfghijklmnpqrsvwxyz-mise-2026.10.0"
	f := newFakeFloor(t)
	f.present[floorArm], f.present[floorIntel] = 0, 0
	f.refs[floorArm] = []string{strings.TrimPrefix(openssl, "/nix/store/")}
	f.upstream[openssl] = true
	g, _ := newTestFloorGate(t, f)
	if _, err := g.wait(context.Background()); err != nil {
		t.Fatalf("wait with the reference upstream: %v", err)
	}
	if f.upstreamReads[openssl] != 1 {
		t.Fatalf("the upstream cache was read %d times for the reference, want once", f.upstreamReads[openssl])
	}

	f = newFakeFloor(t)
	f.present[floorIntel] = 0
	f.upstream[floorArm] = true // a profile upstream is not the project's floor
	f.refs[floorIntel] = []string{strings.TrimPrefix(mise, "/nix/store/")}
	f.afterDispatch = [][]run{{dispatchedRun(40, "completed", "success")}}
	g, _ = newTestFloorGate(t, f)
	_, err := g.wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), floorArm) || !strings.Contains(err.Error(), mise) {
		t.Fatalf("err = %v, want a refusal naming the profile and the reference neither cache serves", err)
	}
	if f.upstreamReads[floorArm] != 0 {
		t.Fatalf("the gate asked the upstream cache for a profile")
	}
}

// A dispatch on a branch at the target (the step this gate named before it
// dispatched for itself) is a run the gate waits for; a push off main is not,
// so the gate dispatches its own build instead.
func TestFloorGateCountsABranchDispatchButNotABranchPush(t *testing.T) {
	for _, tc := range []struct {
		event string
		wait  bool
	}{{"workflow_dispatch", true}, {"push", false}} {
		f := newFakeFloor(t)
		f.present[floorArm], f.present[floorIntel] = 0, 2
		r := floorRun(5, testSHA, "in_progress", "")
		r.Event, r.HeadBranch = tc.event, "darwin-floor-"+testSHA[:12]
		done := r
		done.Status, done.Conclusion = "completed", "success"
		f.runSeq = [][]run{{r}, {done}}
		g, clock := newTestFloorGate(t, f)
		_, err := g.wait(context.Background())
		if tc.wait && (err != nil || clock.sleeps != 1 || f.dispatchCount() != 0) {
			t.Fatalf("%s: err=%v sleeps=%d dispatches=%d; want one wait, no dispatch, then a pass", tc.event, err, clock.sleeps, f.dispatchCount())
		}
		if !tc.wait && f.dispatchCount() != 1 {
			t.Fatalf("%s: a push off main was waited on instead of dispatching a build (dispatches=%d)", tc.event, f.dispatchCount())
		}
	}
}

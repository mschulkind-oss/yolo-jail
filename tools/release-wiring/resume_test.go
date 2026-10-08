package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resumeState is what GitHub, PyPI and the tap report. The zero-adjusted value
// from tagOnlyState is the state 0.12.2 was left in: the request's annotated
// tag at the target, one failed Release run, nothing else.
type resumeState struct {
	refType, tagTarget string
	releases           []claimRelease
	releaseRuns        []resumeRun
	publishRuns        []resumeRun
	requestRuns        []resumeRun
	pypiStatus         int
	pypiBody           string
	formulaStatus      int
	formula            string
}

const resumeTagObject = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"

func tagOnlyState() resumeState {
	return resumeState{
		refType:   "tag",
		tagTarget: testSHA,
		releases:  []claimRelease{{ID: 7, TagName: "v9.8.6"}},
		releaseRuns: []resumeRun{
			{ID: 202, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release v9.8.7 @ " + testSHA + " / request 100", Status: "completed", Conclusion: "failure"},
			{ID: 150, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release v9.8.6 @ " + strings.Repeat("e", 40) + " / request 90", Status: "completed", Conclusion: "success"},
		},
		publishRuns: []resumeRun{{ID: 160, DisplayName: "Publish v9.8.6 @ " + strings.Repeat("e", 40) + " / request 90 / release 150", Status: "completed", Conclusion: "success"}},
		requestRuns: []resumeRun{
			{ID: 100, DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "completed", Conclusion: "failure"},
			{ID: 101, DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "in_progress"},
		},
		pypiStatus:    http.StatusOK,
		pypiBody:      `{"releases":{"9.8.5":[],"9.8.6":[]}}`,
		formulaStatus: http.StatusOK,
		formula:       `url "https://github.com/owner/repo/archive/refs/tags/v9.8.6.tar.gz"`,
	}
}

func resumeServer(t *testing.T, state resumeState) resumeSources {
	t.Helper()
	runs := func(w http.ResponseWriter, list []resumeRun) {
		writeJSON(w, map[string]any{"total_count": len(list), "workflow_runs": list})
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/git/ref/tags/v9.8.7":
			sha := resumeTagObject
			if state.refType == "commit" {
				sha = state.tagTarget
			}
			writeJSON(w, map[string]any{"object": map[string]string{"type": state.refType, "sha": sha}})
		case "/repos/owner/repo/git/tags/" + resumeTagObject:
			writeJSON(w, map[string]any{"object": map[string]string{"type": "commit", "sha": state.tagTarget}})
		case "/repos/owner/repo/releases":
			if r.URL.Query().Get("page") == "1" {
				writeJSON(w, state.releases)
			} else {
				writeJSON(w, []claimRelease{})
			}
		case "/repos/owner/repo/actions/workflows/release.yml":
			writeJSON(w, workflowIdentity{ID: 2, Path: ".github/workflows/release.yml"})
		case "/repos/owner/repo/actions/workflows/publish.yml":
			writeJSON(w, workflowIdentity{ID: 3, Path: ".github/workflows/publish.yml"})
		case "/repos/owner/repo/actions/workflows/release-request.yml":
			writeJSON(w, workflowIdentity{ID: 1, Path: ".github/workflows/release-request.yml"})
		case "/repos/owner/repo/actions/workflows/2/runs":
			runs(w, state.releaseRuns)
		case "/repos/owner/repo/actions/workflows/3/runs":
			runs(w, state.publishRuns)
		case "/repos/owner/repo/actions/workflows/1/runs":
			runs(w, state.requestRuns)
		case "/pypi/yolo-jail/json":
			w.WriteHeader(state.pypiStatus)
			fmt.Fprint(w, state.pypiBody)
		case "/formula":
			w.WriteHeader(state.formulaStatus)
			fmt.Fprint(w, state.formula)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return resumeSources{
		github:     claimClient{apiURL: server.URL, http: server.Client()},
		public:     server.Client(),
		pypiURL:    server.URL + "/pypi/yolo-jail/json",
		formulaURL: server.URL + "/formula",
	}
}

func TestResumeIsAcceptedOnlyForTagOnlyState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*resumeState)
		wantErr string
	}{
		{name: "tag only, after a failed Release run", mutate: func(*resumeState) {}},
		{name: "tag only, project not yet on PyPI", mutate: func(s *resumeState) { s.pypiStatus, s.pypiBody = http.StatusNotFound, "" }},
		{name: "tag at another commit", mutate: func(s *resumeState) { s.tagTarget = strings.Repeat("f", 40) }, wantErr: "not the requested commit"},
		{name: "lightweight tag", mutate: func(s *resumeState) { s.refType = "commit" }, wantErr: "not an annotated tag"},
		{name: "draft release exists", mutate: func(s *resumeState) {
			s.releases = append(s.releases, claimRelease{ID: 9, TagName: "v9.8.7", Draft: true})
		}, wantErr: "draft GitHub Release exists"},
		{name: "release with uploaded assets and claim exists", mutate: func(s *resumeState) {
			s.releases = append(s.releases, claimRelease{ID: 9, TagName: "v9.8.7"})
		}, wantErr: "published GitHub Release exists"},
		{name: "Release run still running", mutate: func(s *resumeState) {
			s.releaseRuns = append(s.releaseRuns, resumeRun{ID: 203, DisplayName: "Release v9.8.7 @ " + testSHA + " / request 100", Status: "in_progress"})
		}, wantErr: "still in_progress"},
		{name: "Release run succeeded", mutate: func(s *resumeState) { s.releaseRuns[0].Conclusion = "success" }, wantErr: "succeeded"},
		{name: "Release run at another commit", mutate: func(s *resumeState) {
			s.releaseRuns[0].DisplayName = "Release v9.8.7 @ " + strings.Repeat("f", 40) + " / request 100"
		}, wantErr: "another commit"},
		{name: "Homebrew-only backfill ran", mutate: func(s *resumeState) {
			s.releaseRuns = append(s.releaseRuns, resumeRun{ID: 204, DisplayName: "Homebrew-only v9.8.7", Status: "completed", Conclusion: "failure"})
		}, wantErr: "tap"},
		{name: "tag-push Release run", mutate: func(s *resumeState) {
			s.releaseRuns = append(s.releaseRuns, resumeRun{ID: 205, Event: "push", HeadBranch: "v9.8.7", DisplayName: "chore(release): 9.8.7", Status: "completed", Conclusion: "failure"})
		}, wantErr: "tap"},
		{name: "publisher run exists", mutate: func(s *resumeState) {
			s.publishRuns = append(s.publishRuns, resumeRun{ID: 300, DisplayName: "Publish v9.8.7 @ " + testSHA + " / request 100 / release 202", Status: "completed", Conclusion: "failure"})
		}, wantErr: "publisher run 300"},
		{name: "another request still running", mutate: func(s *resumeState) {
			s.requestRuns = append(s.requestRuns, resumeRun{ID: 102, DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "queued"})
		}, wantErr: "release request run 102"},
		{name: "PyPI has the version", mutate: func(s *resumeState) { s.pypiBody = `{"releases":{"9.8.6":[],"9.8.7":[]}}` }, wantErr: "PyPI already has"},
		{name: "PyPI unreadable", mutate: func(s *resumeState) { s.pypiStatus = http.StatusServiceUnavailable }, wantErr: "unproven"},
		{name: "tap carries the version", mutate: func(s *resumeState) {
			s.formula = `url "https://github.com/owner/repo/archive/refs/tags/v9.8.7.tar.gz"`
		}, wantErr: "already carries v9.8.7"},
		{name: "tap unreadable", mutate: func(s *resumeState) { s.formulaStatus = http.StatusNotFound }, wantErr: "unproven"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := tagOnlyState()
			tc.mutate(&state)
			err := verifyResume(context.Background(), resumeServer(t, state), "owner/repo", "9.8.7", testSHA, "101")
			if tc.wantErr == "" && err != nil {
				t.Fatalf("tag-only state refused: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("resume error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPEP440LooseFoldsPreReleaseSpellings(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"0.13.0-rc.1", "0.13.0rc1"},
		{"0.13.0-RC1", "0.13.0rc1"},
		{"0.12.2", "0.12.2"},
	} {
		if pep440Loose(tc.a) != pep440Loose(tc.b) {
			t.Errorf("pep440Loose(%q)=%q != pep440Loose(%q)=%q", tc.a, pep440Loose(tc.a), tc.b, pep440Loose(tc.b))
		}
	}
	if pep440Loose("0.12.2") == pep440Loose("0.12.20") {
		t.Error("0.12.2 and 0.12.20 must differ")
	}
}

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseWorkflowRequiresItsExactInProgressTrustedRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     actionRun
		wantErr string
	}{
		{name: "exact request", run: actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "in_progress", Attempt: 1}},
		{name: "wrong workflow", run: actionRun{ID: 101, WorkflowID: 2, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "in_progress", Attempt: 1}, wantErr: "not the exact first in-progress"},
		{name: "wrong exact SHA title", run: actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + strings.Repeat("f", 40), Status: "in_progress", Attempt: 1}, wantErr: "not the exact first in-progress"},
		{name: "completed request cannot replay release", run: actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "completed", Conclusion: "success", Attempt: 1}, wantErr: "not the exact first in-progress"},
		{name: "rerun cannot create release", run: actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "in_progress", Attempt: 2}, wantErr: "not the exact first in-progress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/owner/repo/actions/workflows/release-request.yml":
					writeJSON(w, workflowIdentity{ID: 1, Path: ".github/workflows/release-request.yml"})
				case "/repos/owner/repo/actions/runs/101":
					writeJSON(w, tc.run)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := claimClient{apiURL: server.URL, http: server.Client()}
			err := verifyReleaseRequest(context.Background(), client, "owner/repo", "9.8.7", testSHA, "101")
			if tc.wantErr == "" && err != nil {
				t.Fatalf("exact release request refused: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("release request verification error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPublisherProvenanceRequiresTheOriginalSuccessfulMainWorkflowRuns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request actionRun
		release actionRun
		wantErr string
	}{
		{name: "exact original request and completed release", request: actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "in_progress", Attempt: 1}, release: actionRun{ID: 202, WorkflowID: 2, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release v9.8.7 @ " + testSHA + " / request 101", Status: "completed", Conclusion: "success", Attempt: 1}},
		{name: "request from tag branch", request: actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "v9.8.7", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "in_progress", Attempt: 1}, release: actionRun{ID: 202, WorkflowID: 2, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release v9.8.7 @ " + testSHA + " / request 101", Status: "completed", Conclusion: "success", Attempt: 1}, wantErr: "exact first trusted-main release request"},
		{name: "release title SHA mismatch", request: actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "in_progress", Attempt: 1}, release: actionRun{ID: 202, WorkflowID: 2, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release v9.8.7 @ " + strings.Repeat("f", 40) + " / request 101", Status: "completed", Conclusion: "success", Attempt: 1}, wantErr: "successful first trusted-main GoReleaser"},
		{name: "failed original release", request: actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "in_progress", Attempt: 1}, release: actionRun{ID: 202, WorkflowID: 2, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release v9.8.7 @ " + testSHA + " / request 101", Status: "completed", Conclusion: "failure", Attempt: 1}, wantErr: "successful first trusted-main GoReleaser"},
		{name: "rerun is not initial publication", request: actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "in_progress", Attempt: 1}, release: actionRun{ID: 202, WorkflowID: 2, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release v9.8.7 @ " + testSHA + " / request 101", Status: "completed", Conclusion: "success", Attempt: 2}, wantErr: "successful first trusted-main GoReleaser"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/owner/repo/actions/workflows/release-request.yml":
					writeJSON(w, workflowIdentity{ID: 1, Path: ".github/workflows/release-request.yml"})
				case "/repos/owner/repo/actions/workflows/release.yml":
					writeJSON(w, workflowIdentity{ID: 2, Path: ".github/workflows/release.yml"})
				case "/repos/owner/repo/actions/runs/101":
					writeJSON(w, tc.request)
				case "/repos/owner/repo/actions/runs/202":
					writeJSON(w, tc.release)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := claimClient{apiURL: server.URL, http: server.Client()}
			err := verifyPublisherProvenance(context.Background(), client, "owner/repo", "9.8.7", testSHA, "101", "202")
			if tc.wantErr == "" && err != nil {
				t.Fatalf("valid provenance refused: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("provenance error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestMainScopedReleaseCallerUsesAnchoredRunNamesAndTrustedMainInputs(t *testing.T) {
	root := repositoryRoot(t)
	for path, wants := range map[string][]string{
		".github/workflows/release-request.yml": {"run-name:", "Release request v", "permissions:", "prepare-target:", "contents: read", "tools/release-wiring/request.sh"},
		".github/workflows/release.yml":         {"run-name:", "Release v", "Homebrew-only v", "verify-release-request", "--skip=publish", "release-assets"},
		".github/workflows/publish.yml":         {"run-name:", "workflow_dispatch:", "environment: pypi", "id-token: write", "--trusted-publishing always", "claim-publication", "check-version-order", "verify-publisher-provenance"},
	} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s does not contain actual caller contract %q", path, want)
			}
		}
	}
}

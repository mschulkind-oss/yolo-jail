package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var provenanceRunID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

type workflowIdentity struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

type actionRun struct {
	ID          int64  `json:"id"`
	WorkflowID  int64  `json:"workflow_id"`
	Event       string `json:"event"`
	HeadBranch  string `json:"head_branch"`
	DisplayName string `json:"display_title"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	Attempt     int    `json:"run_attempt"`
}

func verifyReleaseRequest(ctx context.Context, client claimClient, repo, version, sha, requestID string) error {
	if !releaseRepositoryPattern.MatchString(repo) || strings.Contains(repo, "..") ||
		!releaseVersionPattern.MatchString(version) || !releaseCommitSHA.MatchString(sha) ||
		!provenanceRunID.MatchString(requestID) || parseRunID(requestID) <= 0 {
		return fmt.Errorf("release request repository, version, SHA, or run id is malformed")
	}
	requestWorkflow, err := workflowForPath(ctx, client, repo, ".github/workflows/release-request.yml")
	if err != nil {
		return err
	}
	request, err := actionRunForID(ctx, client, repo, requestID)
	if err != nil {
		return fmt.Errorf("cannot verify originating release-request run: %w", err)
	}
	wantTitle := "Release request v" + version + " @ " + sha
	if request.ID != parseRunID(requestID) || request.WorkflowID != requestWorkflow.ID ||
		requestWorkflow.Path != ".github/workflows/release-request.yml" || request.Event != "workflow_dispatch" ||
		request.HeadBranch != "main" || request.DisplayName != wantTitle || request.Status != "in_progress" || request.Attempt != 1 {
		return fmt.Errorf("run %s is not the exact first in-progress trusted-main release request for %s at %s", requestID, version, sha)
	}
	return nil
}

func verifyReleaseRequestFromEnv(ctx context.Context, out io.Writer) error {
	repo, version, sha, requestID := os.Getenv("GITHUB_REPOSITORY"), os.Getenv("RELEASE_VERSION"), os.Getenv("RELEASE_SHA"), os.Getenv("REQUEST_RUN_ID")
	if os.Getenv("GITHUB_REF_TYPE") != "branch" || os.Getenv("GITHUB_REF_NAME") != "main" ||
		os.Getenv("GITHUB_EVENT_NAME") != "workflow_dispatch" || os.Getenv("GITHUB_RUN_ATTEMPT") != "1" {
		return fmt.Errorf("release creation must run on the first main-scoped workflow_dispatch attempt")
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("a read-only GitHub token is required to verify the originating release request")
	}
	apiURL, err := githubAPIBaseFromEnv()
	if err != nil {
		return err
	}
	client := claimClient{apiURL: apiURL, token: token, http: newGitHubHTTPClient()}
	if err := verifyReleaseRequest(ctx, client, repo, version, sha, requestID); err != nil {
		return err
	}
	fmt.Fprintf(out, "Verified in-progress release request %s for v%s at %s.\n", requestID, version, sha)
	return nil
}

// verifyPublisherProvenance binds the publisher to the main-sourced request and
// the successful original main-sourced Release run. A tag or release object by
// itself is not publication authorization.
func verifyPublisherProvenance(ctx context.Context, client claimClient, repo, version, sha, requestID, releaseID string) error {
	if !releaseRepositoryPattern.MatchString(repo) || strings.Contains(repo, "..") {
		return fmt.Errorf("publisher repository must be OWNER/REPO")
	}
	if !releaseVersionPattern.MatchString(version) || !releaseCommitSHA.MatchString(sha) || !provenanceRunID.MatchString(requestID) || !provenanceRunID.MatchString(releaseID) || parseRunID(requestID) <= 0 || parseRunID(releaseID) <= 0 {
		return fmt.Errorf("publisher version, full SHA, or originating run id is malformed")
	}
	requestWorkflow, err := workflowForPath(ctx, client, repo, ".github/workflows/release-request.yml")
	if err != nil {
		return err
	}
	releaseWorkflow, err := workflowForPath(ctx, client, repo, ".github/workflows/release.yml")
	if err != nil {
		return err
	}
	request, err := actionRunForID(ctx, client, repo, requestID)
	if err != nil {
		return fmt.Errorf("cannot verify originating request run: %w", err)
	}
	release, err := actionRunForID(ctx, client, repo, releaseID)
	if err != nil {
		return fmt.Errorf("cannot verify original GoReleaser run: %w", err)
	}
	requestTitle := "Release request v" + version + " @ " + sha
	releaseTitle := "Release v" + version + " @ " + sha + " / request " + requestID
	if request.ID != parseRunID(requestID) || request.WorkflowID != requestWorkflow.ID || requestWorkflow.Path != ".github/workflows/release-request.yml" ||
		request.Event != "workflow_dispatch" || request.HeadBranch != "main" || request.DisplayName != requestTitle || request.Attempt != 1 {
		return fmt.Errorf("run %s is not the exact first trusted-main release request for %s at %s", requestID, version, sha)
	}
	if request.Status == "completed" {
		if request.Conclusion != "success" {
			return fmt.Errorf("originating request run %s concluded %s; no publication claim is safe", requestID, request.Conclusion)
		}
	} else if request.Status != "in_progress" {
		return fmt.Errorf("originating request run %s has unknown status %q; no publication claim is safe", requestID, request.Status)
	}
	if release.ID != parseRunID(releaseID) || release.WorkflowID != releaseWorkflow.ID || releaseWorkflow.Path != ".github/workflows/release.yml" ||
		release.Event != "workflow_dispatch" || release.HeadBranch != "main" || release.DisplayName != releaseTitle ||
		release.Status != "completed" || release.Conclusion != "success" || release.Attempt != 1 {
		return fmt.Errorf("run %s is not the successful first trusted-main GoReleaser run for %s at %s; preserve the tag and inspect state read-only", releaseID, version, sha)
	}
	return nil
}

func workflowForPath(ctx context.Context, client claimClient, repo, path string) (workflowIdentity, error) {
	var workflow workflowIdentity
	if err := client.getJSON(ctx, client.apiURL+"/repos/"+repo+"/actions/workflows/"+strings.TrimPrefix(path, ".github/workflows/"), &workflow); err != nil {
		return workflow, fmt.Errorf("cannot verify trusted workflow %s: %w", path, err)
	}
	if workflow.ID <= 0 || workflow.Path != path {
		return workflow, fmt.Errorf("GitHub workflow identity for %s is missing or mismatched", path)
	}
	return workflow, nil
}

func actionRunForID(ctx context.Context, client claimClient, repo, id string) (actionRun, error) {
	var run actionRun
	if err := client.getJSON(ctx, client.apiURL+"/repos/"+repo+"/actions/runs/"+id, &run); err != nil {
		return run, err
	}
	return run, nil
}

func parseRunID(raw string) int64 {
	value, _ := strconv.ParseInt(raw, 10, 64)
	return value
}

func verifyPublisherProvenanceFromEnv(ctx context.Context, out io.Writer) error {
	repo, version := os.Getenv("GITHUB_REPOSITORY"), os.Getenv("RELEASE_VERSION")
	sha, requestID, releaseID := os.Getenv("RELEASE_SHA"), os.Getenv("REQUEST_RUN_ID"), os.Getenv("RELEASE_RUN_ID")
	if !releaseRepositoryPattern.MatchString(repo) || strings.Contains(repo, "..") {
		return fmt.Errorf("GITHUB_REPOSITORY must be OWNER/REPO")
	}
	if !provenanceRunID.MatchString(requestID) || !provenanceRunID.MatchString(releaseID) || parseRunID(requestID) <= 0 || parseRunID(releaseID) <= 0 {
		return fmt.Errorf("originating Actions run ids are outside the supported positive integer range")
	}
	if os.Getenv("GITHUB_REF_TYPE") != "branch" || os.Getenv("GITHUB_REF_NAME") != "main" || os.Getenv("GITHUB_EVENT_NAME") != "workflow_dispatch" || os.Getenv("GITHUB_RUN_ATTEMPT") != "1" {
		return fmt.Errorf("publisher must be the first main-scoped workflow_dispatch attempt")
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("a read-only GitHub token is required to verify publisher provenance")
	}
	apiURL, err := githubAPIBaseFromEnv()
	if err != nil {
		return err
	}
	client := claimClient{apiURL: apiURL, token: token, http: newGitHubHTTPClient()}
	if err := verifyPublisherProvenance(ctx, client, repo, version, sha, requestID, releaseID); err != nil {
		return err
	}
	fmt.Fprintf(out, "Verified the first main-scoped request and successful original Release run for v%s at %s.\n", version, sha)
	return nil
}

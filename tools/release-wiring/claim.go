package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const publicationClaimAsset = "yolo-publication-claim.json"

var runIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

type publicationClaim struct {
	SchemaVersion int    `json:"schema_version"`
	Tag           string `json:"tag"`
	Version       string `json:"version"`
	SHA           string `json:"sha"`
	RequestRunID  string `json:"request_run_id"`
	ReleaseRunID  string `json:"release_run_id"`
	PublishRunID  string `json:"publish_run_id"`
	RunAttempt    int    `json:"run_attempt"`
}

type claimRelease struct {
	ID        int64  `json:"id"`
	TagName   string `json:"tag_name"`
	Draft     bool   `json:"draft"`
	UploadURL string `json:"upload_url"`
}

type gitRef struct {
	Object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
	} `json:"object"`
}

type gitTag struct {
	Message string `json:"message"`
	Object  struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
	} `json:"object"`
}

type claimClient struct {
	apiURL     string
	uploadHost string
	token      string
	http       *http.Client
}

func claimFromEnv(ctx context.Context, out io.Writer) error {
	return claimFromEnvWithHTTP(ctx, out, nil)
}

func claimFromEnvWithHTTP(ctx context.Context, out io.Writer, httpClient *http.Client) error {
	get := func(name string) (string, error) {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		return value, nil
	}
	repo, err := get("GITHUB_REPOSITORY")
	if err != nil {
		return err
	}
	version, err := get("RELEASE_VERSION")
	if err != nil {
		return err
	}
	sha, err := get("RELEASE_SHA")
	if err != nil {
		return err
	}
	requestID, err := get("REQUEST_RUN_ID")
	if err != nil {
		return err
	}
	releaseID, err := get("RELEASE_RUN_ID")
	if err != nil {
		return err
	}
	publishID, err := get("GITHUB_RUN_ID")
	if err != nil {
		return err
	}
	attempt, err := get("GITHUB_RUN_ATTEMPT")
	if err != nil {
		return err
	}
	attemptNumber, err := strconv.Atoi(attempt)
	if err != nil || attemptNumber != 1 {
		return errors.New("publication claim refuses a rerun; preserve the release and inspect the original publisher run")
	}
	for name, value := range map[string]string{"REQUEST_RUN_ID": requestID, "RELEASE_RUN_ID": releaseID, "GITHUB_RUN_ID": publishID} {
		if !runIDPattern.MatchString(value) || parseRunID(value) <= 0 {
			return fmt.Errorf("%s must be a positive Actions run id", name)
		}
	}
	if os.Getenv("GITHUB_REF_TYPE") != "branch" || os.Getenv("GITHUB_REF_NAME") != "main" || os.Getenv("GITHUB_EVENT_NAME") != "workflow_dispatch" {
		return errors.New("publication claim must run in the trusted-main workflow_dispatch context")
	}
	if !releaseCommitSHA.MatchString(sha) {
		return errors.New("RELEASE_SHA must be a full lowercase commit SHA")
	}
	if !releaseVersionPattern.MatchString(version) {
		return errors.New("RELEASE_VERSION must be X.Y.Z or X.Y.Z-pre")
	}
	if !releaseRepositoryPattern.MatchString(repo) || strings.Contains(repo, "..") {
		return errors.New("GITHUB_REPOSITORY must be OWNER/REPO")
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return errors.New("a GitHub token with contents:write is required for the one-time publication claim")
	}
	apiURL, err := githubAPIBaseFromEnv()
	if err != nil {
		return err
	}
	uploadURL := strings.TrimRight(os.Getenv("GITHUB_UPLOADS_URL"), "/")
	if uploadURL == "" {
		uploadURL = "https://uploads.github.com"
	}
	if httpClient == nil {
		httpClient = newGitHubHTTPClient()
	}
	client := claimClient{
		apiURL: apiURL, uploadHost: uploadURL, token: token,
		http: httpClient,
	}
	claim := publicationClaim{
		SchemaVersion: 1,
		Tag:           "v" + version,
		Version:       version,
		SHA:           sha,
		RequestRunID:  requestID,
		ReleaseRunID:  releaseID,
		PublishRunID:  publishID,
		RunAttempt:    attemptNumber,
	}
	if err := client.create(ctx, repo, claim); err != nil {
		return err
	}
	fmt.Fprintf(out, "Created release asset %s for %s at %s; this one-time claim is workflow-retained, not administrator-immutable.\n", publicationClaimAsset, claim.Tag, claim.SHA)
	return nil
}

func (c claimClient) create(ctx context.Context, repo string, claim publicationClaim) error {
	if !releaseCommitSHA.MatchString(claim.SHA) || !releaseVersionPattern.MatchString(claim.Version) || claim.Tag != "v"+claim.Version {
		return errors.New("claim tag/version/SHA tuple is invalid")
	}
	resolved, err := c.resolveTag(ctx, repo, claim.Tag)
	if err != nil {
		return fmt.Errorf("cannot verify immutable tag %s: %w", claim.Tag, err)
	}
	if resolved != claim.SHA {
		return fmt.Errorf("tag %s resolves to %s, not requested %s; no claim was written", claim.Tag, resolved, claim.SHA)
	}

	var release claimRelease
	if err := c.getJSON(ctx, c.apiURL+"/repos/"+repo+"/releases/tags/"+url.PathEscape(claim.Tag), &release); err != nil {
		return fmt.Errorf("cannot verify the completed GitHub release for %s; no claim was written: %w", claim.Tag, err)
	}
	if release.ID <= 0 || release.TagName != claim.Tag || release.Draft {
		return fmt.Errorf("GitHub release for %s is missing, mismatched, or draft; wait for the exact original Release run to succeed", claim.Tag)
	}
	uploadBase, err := url.Parse(c.uploadHost)
	if err != nil || uploadBase.Scheme != "https" || uploadBase.Host == "" || uploadBase.User != nil || uploadBase.RawQuery != "" || uploadBase.Fragment != "" {
		return errors.New("configured GitHub asset upload endpoint is invalid")
	}
	upload, err := url.Parse(strings.TrimSuffix(release.UploadURL, "{?name,label}"))
	if err != nil || upload.Scheme != "https" || upload.Host != uploadBase.Host || upload.User != nil || upload.RawQuery != "" || upload.Fragment != "" {
		return errors.New("GitHub returned an unexpected release asset upload URL; no claim was written")
	}
	wantPath := "/repos/" + repo + "/releases/" + strconv.FormatInt(release.ID, 10) + "/assets"
	if path.Clean(upload.Path) != wantPath {
		return errors.New("GitHub release asset upload URL does not match the validated release id")
	}
	body, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	assetURL := uploadBase.Scheme + "://" + uploadBase.Host + wantPath + "?name=" + url.QueryEscape(publicationClaimAsset)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, assetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("claim upload outcome is unknown; do not retry or remove the asset; inspect %s read-only with the owner: %w", claim.Tag, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	switch response.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("release already has an asset named %s (GitHub refuses duplicate upload names); preserve %s and inspect the original run and claim asset; no build or registry write is allowed", publicationClaimAsset, claim.Tag)
	default:
		return fmt.Errorf("claim upload returned HTTP %d; outcome may be partial or unknown; preserve %s, do not retry, and inspect the original run and asset read-only", response.StatusCode, claim.Tag)
	}
}

func (c claimClient) resolveTag(ctx context.Context, repo, tag string) (string, error) {
	var ref gitRef
	if err := c.getJSON(ctx, c.apiURL+"/repos/"+repo+"/git/ref/tags/"+url.PathEscape(tag), &ref); err != nil {
		return "", err
	}
	switch ref.Object.Type {
	case "commit":
		return ref.Object.SHA, nil
	case "tag":
		var annotated gitTag
		if err := c.getJSON(ctx, c.apiURL+"/repos/"+repo+"/git/tags/"+ref.Object.SHA, &annotated); err != nil {
			return "", err
		}
		if annotated.Object.Type != "commit" {
			return "", fmt.Errorf("annotated tag points to %q rather than a commit", annotated.Object.Type)
		}
		return annotated.Object.SHA, nil
	default:
		return "", fmt.Errorf("tag points to unexpected Git object type %q", ref.Object.Type)
	}
}

func (c claimClient) getJSON(ctx context.Context, address string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("GitHub API returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode GitHub API response: %w", err)
	}
	return nil
}

var releaseVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
var releaseRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestGitHubAPIRefusesRedirectsAndCredentialBearingBaseURLs(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("redirect received Authorization header %q", got)
		}
	}))
	defer target.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer first.Close()
	request, err := http.NewRequest(http.MethodGet, first.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer offline-test-token")
	response, err := newGitHubHTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound || redirected.Load() != 0 {
		t.Fatalf("redirect response = %d, target requests = %d; want a refused redirect", response.StatusCode, redirected.Load())
	}

	t.Setenv("GITHUB_API_URL", "https://private-user:private-password@example.invalid/api")
	base, err := githubAPIBaseFromEnv()
	if err == nil || base != "" || strings.Contains(err.Error(), "private-user") || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("credential-bearing API URL result = %q, %v; want a redacted refusal", base, err)
	}
}

func TestCreateOnlyPublicationClaimIsAtomicAndBindsProvenance(t *testing.T) {
	var mu sync.Mutex
	assets := map[string][]byte{}
	var uploads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer offline-test-token" {
			http.Error(w, "wrong authorization", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/git/ref/tags/v9.8.7":
			writeJSON(w, map[string]any{"object": map[string]any{"type": "tag", "sha": "tagobject"}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/git/tags/tagobject":
			writeJSON(w, map[string]any{"object": map[string]any{"type": "commit", "sha": testSHA}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/releases/tags/v9.8.7":
			writeJSON(w, map[string]any{"id": 77, "tag_name": "v9.8.7", "draft": false,
				"upload_url": serverURL(r) + "/repos/owner/repo/releases/77/assets{?name,label}"})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/releases/77/assets":
			uploads.Add(1)
			if r.URL.Query().Get("name") != publicationClaimAsset || r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "wrong asset upload request", http.StatusBadRequest)
				return
			}
			var body publicationClaim
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad claim JSON", http.StatusBadRequest)
				return
			}
			if body.Tag != "v9.8.7" || body.Version != "9.8.7" || body.SHA != testSHA || body.RequestRunID != "101" || body.ReleaseRunID != "202" || body.PublishRunID != "303" || body.RunAttempt != 1 {
				http.Error(w, "claim did not bind validated provenance", http.StatusBadRequest)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if _, ok := assets[publicationClaimAsset]; ok {
				http.Error(w, "asset exists", http.StatusUnprocessableEntity)
				return
			}
			data, _ := json.Marshal(body)
			assets[publicationClaimAsset] = data
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := claimClient{apiURL: server.URL, uploadHost: server.URL, token: "offline-test-token", http: server.Client()}
	claim := validTestClaim()
	if err := client.create(context.Background(), "owner/repo", claim); err != nil {
		t.Fatalf("initial claim refused: %v", err)
	}
	mu.Lock()
	var saved publicationClaim
	err := json.Unmarshal(assets[publicationClaimAsset], &saved)
	mu.Unlock()
	if err != nil || saved != claim {
		t.Fatalf("claim asset = %+v, decode error %v; want %+v", saved, err, claim)
	}
	if err := client.create(context.Background(), "owner/repo", claim); err == nil || !strings.Contains(err.Error(), "already has an asset named") {
		t.Fatalf("duplicate claim = %v, want a create-only conflict refusal", err)
	}
	if got := uploads.Load(); got != 2 {
		t.Fatalf("upload requests = %d, want one successful create and one conflict", got)
	}
}

func TestConcurrentPublicationClaimsHaveOneWinner(t *testing.T) {
	var mu sync.Mutex
	created := false
	var writes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/git/ref/tags/v9.8.7") {
			writeJSON(w, map[string]any{"object": map[string]any{"type": "commit", "sha": testSHA}})
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/releases/tags/v9.8.7") {
			writeJSON(w, map[string]any{"id": 77, "tag_name": "v9.8.7", "draft": false,
				"upload_url": serverURL(r) + "/repos/owner/repo/releases/77/assets{?name,label}"})
			return
		}
		if r.Method == http.MethodPost {
			writes.Add(1)
			mu.Lock()
			defer mu.Unlock()
			if created {
				http.Error(w, "asset exists", http.StatusUnprocessableEntity)
				return
			}
			created = true
			w.WriteHeader(http.StatusCreated)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := claimClient{apiURL: server.URL, uploadHost: server.URL, token: "offline-test-token", http: server.Client()}
	claim := validTestClaim()
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- client.create(context.Background(), "owner/repo", claim) }()
	}
	var succeeded, refused int
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			succeeded++
		} else if strings.Contains(err.Error(), "already has an asset named") {
			refused++
		} else {
			t.Errorf("unexpected concurrent claim result: %v", err)
		}
	}
	if succeeded != 1 || refused != 1 || writes.Load() != 2 {
		t.Fatalf("concurrent claims: success=%d duplicate=%d writes=%d, want 1/1/2", succeeded, refused, writes.Load())
	}
}

func TestPublicationClaimFailsClosedOnWrongTagDraftOrAmbiguousUpload(t *testing.T) {
	for _, tc := range []struct {
		name       string
		refSHA     string
		draft      bool
		uploadCode int
		want       string
	}{
		{name: "wrong tag SHA", refSHA: strings.Repeat("f", 40), want: "not requested"},
		{name: "draft release", refSHA: testSHA, draft: true, want: "draft"},
		{name: "ambiguous upload", refSHA: testSHA, uploadCode: http.StatusInternalServerError, want: "outcome may be partial or unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/git/ref/tags/v9.8.7") {
					writeJSON(w, map[string]any{"object": map[string]any{"type": "commit", "sha": tc.refSHA}})
					return
				}
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/releases/tags/v9.8.7") {
					writeJSON(w, map[string]any{"id": 77, "tag_name": "v9.8.7", "draft": tc.draft,
						"upload_url": serverURL(r) + "/repos/owner/repo/releases/77/assets{?name,label}"})
					return
				}
				if r.Method == http.MethodPost {
					posts.Add(1)
					http.Error(w, "unknown", tc.uploadCode)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			client := claimClient{apiURL: server.URL, uploadHost: server.URL, token: "offline-test-token", http: server.Client()}
			err := client.create(context.Background(), "owner/repo", validTestClaim())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("claim error = %v, want %q", err, tc.want)
			}
			if tc.uploadCode == 0 && posts.Load() != 0 {
				t.Fatalf("refusal before publication made %d asset uploads", posts.Load())
			}
			if tc.uploadCode != 0 && posts.Load() != 1 {
				t.Fatalf("ambiguous upload was retried %d times", posts.Load())
			}
		})
	}
}

func TestClaimFromEnvironmentRefusesRerunBeforeAPI(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")
	t.Setenv("RELEASE_VERSION", "9.8.7")
	t.Setenv("RELEASE_SHA", testSHA)
	t.Setenv("REQUEST_RUN_ID", "101")
	t.Setenv("RELEASE_RUN_ID", "202")
	t.Setenv("GITHUB_RUN_ID", "303")
	t.Setenv("GITHUB_RUN_ATTEMPT", "2")
	t.Setenv("GH_TOKEN", "offline-test-token")
	var output strings.Builder
	if err := claimFromEnv(context.Background(), &output); err == nil || !strings.Contains(err.Error(), "refuses a rerun") {
		t.Fatalf("rerun result = %v, want refusal", err)
	}
	if output.Len() != 0 {
		t.Fatalf("rerun wrote output: %s", output.String())
	}
}

func validTestClaim() publicationClaim {
	return publicationClaim{SchemaVersion: 1, Tag: "v9.8.7", Version: "9.8.7", SHA: testSHA,
		RequestRunID: "101", ReleaseRunID: "202", PublishRunID: "303", RunAttempt: 1}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(err)
	}
}

func serverURL(r *http.Request) string {
	return "https://" + r.Host
}

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompareReleaseVersionsUsesSemverPrecedence(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.12.2", "0.12.1", 1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.10", "1.0.0-rc.2", 1},
		{"1.0.0-rc.1", "1.0.0-rc.1", 0},
		{"0.13.0-rc.1", "0.12.9", 1},
		{"0.12.1", "0.12.2", -1},
	} {
		got, err := compareReleaseVersions(tc.a, tc.b)
		if err != nil || (got < 0) != (tc.want < 0) || (got > 0) != (tc.want > 0) {
			t.Errorf("compareReleaseVersions(%q, %q) = %d, %v; want sign %d", tc.a, tc.b, got, err, tc.want)
		}
	}
}

func TestPublishedVersionGateRejectsOlderOrAmbiguousRemoteState(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		version      string
		allowCurrent bool
		wantErr      string
	}{
		{name: "newer than published", body: `[{"tag_name":"v0.12.1","draft":false}]`, version: "0.12.2"},
		{name: "same semantic version with a second spelling is duplicate", body: `[{"tag_name":"v00.12.2","draft":false}]`, version: "0.12.2", wantErr: "not newer"},
		{name: "old version replay", body: `[{"tag_name":"v0.13.0","draft":false}]`, version: "0.12.2", wantErr: "not newer"},
		{name: "legacy version without a claim is still monotonic", body: `[{"tag_name":"v0.13.0","draft":false}]`, version: "0.12.2", wantErr: "not newer"},
		{name: "unknown published version", body: `[{"tag_name":"latest","draft":false}]`, version: "0.14.0", wantErr: "unrecognized version"},
		{name: "draft does not establish published order", body: `[{"tag_name":"v9.0.0","draft":true}]`, version: "0.14.0"},
		{name: "an existing candidate is duplicate before release creation", body: `[{"tag_name":"v0.14.0","draft":false}]`, version: "0.14.0", wantErr: "already published"},
		{name: "publisher may validate its exact current latest release", body: `[{"tag_name":"v0.14.0","draft":false},{"tag_name":"v0.13.0","draft":false}]`, version: "0.14.0", allowCurrent: true},
		{name: "publisher cannot use current release to replay an older version", body: `[{"tag_name":"v0.12.0","draft":false},{"tag_name":"v0.13.0","draft":false}]`, version: "0.12.0", allowCurrent: true, wantErr: "not newer"},
		{name: "duplicate current entries are ambiguous", body: `[{"tag_name":"v0.14.0","draft":false},{"tag_name":"v0.14.0","draft":false}]`, version: "0.14.0", allowCurrent: true, wantErr: "duplicate current tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/owner/repo/releases" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := claimClient{apiURL: server.URL, http: server.Client()}
			err := requireNewestPublishedVersionAllowCurrent(context.Background(), client, "owner/repo", tc.version, tc.allowCurrent)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("version order refused: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("version order error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

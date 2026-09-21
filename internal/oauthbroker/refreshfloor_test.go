package oauthbroker

import (
	"testing"
	"time"
)

// TestRefreshFloorExceedsTheConsumersDueThreshold pins the INEQUALITY, which is
// the whole property. The two floors may move; RefreshCacheFloorMS being above
// ConsumerRefreshDueMS may not, because below it the broker's only possible
// answer to a refresh request is the token the requester has already judged
// stale — and Claude counts an unchanged token as a failed refresh, throwing
// `api_request_oauth_refresh_exhausted` at the second one.
func TestRefreshFloorExceedsTheConsumersDueThreshold(t *testing.T) {
	if RefreshCacheFloorMS <= ConsumerRefreshDueMS {
		t.Fatalf("RefreshCacheFloorMS (%d) must EXCEED ConsumerRefreshDueMS (%d): "+
			"a refresh answered from a cache below the caller's own threshold returns "+
			"the token the caller just rejected", RefreshCacheFloorMS, ConsumerRefreshDueMS)
	}
	// The liveness floor is a different question and is allowed to sit below it;
	// what must not happen is the refresh path quietly adopting it again.
	if LiveTokenFloorMS >= RefreshCacheFloorMS {
		t.Errorf("LiveTokenFloorMS (%d) should sit below RefreshCacheFloorMS (%d)",
			LiveTokenFloorMS, RefreshCacheFloorMS)
	}
}

// TestTheDoughnutIsClosed is the behavioural half, written as the defect it
// fixes: a token inside the window where Claude calls itself due but the old
// 90s floor still called the token fresh. That window returned HTTP 200 with the
// caller's own access_token, and 364 of 380 measured replies landed in it.
//
// It asserts the two paths DISAGREE there, which is the point of splitting them:
// `cached` still reports a usable token, `refresh` declines to serve one.
func TestTheDoughnutIsClosed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	saved := nowFunc
	nowFunc = func() int64 { return now.UnixMilli() }
	t.Cleanup(func() { nowFunc = saved })

	for _, tc := range []struct {
		name           string
		remaining      time.Duration
		wantRefreshHit bool
		wantCachedHit  bool
	}{
		{"deep inside the old doughnut", 200 * time.Second, false, true},
		{"just under the consumer threshold", 299 * time.Second, false, true},
		{"just over the refresh floor", 361 * time.Second, true, true},
		{"a freshly minted token", 8 * time.Hour, true, true},
		{"already expired", -1 * time.Second, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeCreds(t, dir, now.Add(tc.remaining).UnixMilli(), 0, now)
			if got := cachedForRefresh(p) != nil; got != tc.wantRefreshHit {
				t.Errorf("cachedForRefresh hit = %v, want %v (%v remaining): a false "+
					"hit here hands the caller back its own token", got, tc.wantRefreshHit, tc.remaining)
			}
			if got := CachedTokens(p) != nil; got != tc.wantCachedHit {
				t.Errorf("CachedTokens hit = %v, want %v (%v remaining)", got, tc.wantCachedHit, tc.remaining)
			}
		})
	}
}

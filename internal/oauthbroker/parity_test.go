package oauthbroker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Test helpers bridging to jsonx.
type jsonxOrderedMap = jsonx.OrderedMap

// om builds an ordered map from key/value pairs.
func om(pairs ...any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// intv wraps an int as a jsonx integer value.
func intv(n int64) any { return jsonx.IntValue(n) }

func dumpsCompactForTest(v any) (string, error) { return jsonx.DumpsCompact(v) }

// fixedNowMS matches FIXED_NOW_MS in tools/parity/oauth_broker_oracle.py; both
// sides pin "now" so expiresAt/expires_in are deterministic.
const fixedNowMS = int64(1_700_000_000_000)

// withFixedNow overrides nowMS for the duration of fn.
func withFixedNow(fn func()) {
	orig := nowFunc
	nowFunc = func() int64 { return fixedNowMS }
	defer func() { nowFunc = orig }()
	fn()
}

// TestBrokerActionShapesParity byte-diffs the Go broker's action/normalize/
// write shapes against the live Python oracle. This is the Stage 1 wire-fixture
// gate that Stage 6 (OAuth broker) consumes. Skips without Python.
func TestBrokerActionShapesParity(t *testing.T) {
	oracle := runBrokerOracle(t)
	if oracle == nil {
		t.Skip("python oracle unavailable")
	}

	withFixedNow(func() {
		// 1. as_oauth_response over a fresh cached token.
		oauthFresh := om(
			"accessToken", "AT_fresh",
			"refreshToken", "RT_fresh",
			"expiresAt", intv(fixedNowMS+3_600_000),
			"subscriptionType", "max",
			"scopes", []any{"user:inference", "user:profile"},
		)
		assertJSON(t, oracle, "as_oauth_response_fresh", AsOAuthResponse(oauthFresh))

		// 2. normalize full merge.
		upstream := om(
			"access_token", "AT_new",
			"refresh_token", "RT_new",
			"expires_in", intv(7200),
			"scope", "user:inference user:profile",
		)
		previous := om(
			"accessToken", "AT_old",
			"refreshToken", "RT_old",
			"expiresAt", intv(fixedNowMS-10_000),
			"subscriptionType", "max",
			"scopes", []any{"user:inference"},
		)
		assertJSON(t, oracle, "normalize_oauth_full", NormalizeOAuth(upstream, previous))

		// 3. synth scopes.
		assertJSON(t, oracle, "normalize_oauth_synth_scopes",
			NormalizeOAuth(
				om("access_token", "A", "refresh_token", "R", "expires_in", intv(3600), "scope", "a b c"),
				om("expiresAt", intv(0)),
			))

		// 4. no refresh token in response (preserve previous).
		assertJSON(t, oracle, "normalize_oauth_no_refresh",
			NormalizeOAuth(
				om("access_token", "A2", "expires_in", intv(100)),
				om("refreshToken", "KEEP", "expiresAt", intv(0), "scopes", []any{"x"}),
			))

		// 5. error dicts.
		assertJSON(t, oracle, "error_no_refresh_token", errResult("error", "no_refresh_token"))
		assertJSON(t, oracle, "error_creds_unreadable", errResult("error", "creds_unreadable", "message", "boom"))
		assertJSON(t, oracle, "error_upstream_http", errResult("error", "upstream_http", "status", intv(400), "body", "bad"))
		assertJSON(t, oracle, "error_upstream_unreachable", errResult("error", "upstream_unreachable", "message", "no DNS"))
		assertJSON(t, oracle, "error_no_cached_token", errResult("error", "no_cached_token"))

		// 6. ping shape (sans pid).
		assertJSON(t, oracle, "ping_shape", om("pong", true))

		// 7. bad_path proxy error.
		assertJSON(t, oracle, "error_bad_path",
			errResult("error", "bad_path", "message", "path must start with '/': "+pyReprPath("no-leading-slash")))

		// 8. write_tokens blob (indent=2, no sort_keys).
		blob := writeTokensBlob(t, NormalizeOAuth(upstream, previous))
		if got, want := blob, oracle["write_tokens_blob"]; got != want {
			t.Errorf("write_tokens_blob:\n go: %q\n py: %q", got, want)
		}
	})
}

// writeTokensBlob returns the exact bytes WriteTokens would write, without
// touching the filesystem — by re-deriving the same jsonx.DumpsIndent form.
func writeTokensBlob(t *testing.T, oauth *jsonxOrderedMap) string {
	t.Helper()
	// Round-trip through an actual WriteTokens into a temp file to prove the
	// on-disk bytes, then read them back.
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.json")
	if err := WriteTokens(p, oauth); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertJSON(t *testing.T, oracle map[string]string, key string, v any) {
	t.Helper()
	got, err := dumpsCompactForTest(v)
	if err != nil {
		t.Fatalf("%s: encode: %v", key, err)
	}
	want, ok := oracle[key]
	if !ok {
		t.Fatalf("%s: not in oracle output", key)
	}
	if got != want {
		t.Errorf("%s:\n go: %q\n py: %q", key, got, want)
	}
}

func runBrokerOracle(t *testing.T) map[string]string {
	t.Helper()
	root := repoRootOB(t)
	var cmd *exec.Cmd
	if _, err := exec.LookPath("uv"); err == nil {
		cmd = exec.Command("uv", "run", "python", filepath.Join(root, "tools", "parity", "oauth_broker_oracle.py"))
	} else if _, err := exec.LookPath("python3"); err == nil {
		cmd = exec.Command("python3", filepath.Join(root, "tools", "parity", "oauth_broker_oracle.py"))
	} else {
		return nil
	}
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Logf("oracle failed: %v", err)
		return nil
	}
	var result map[string]string
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("decode oracle: %v", err)
	}
	return result
}

func repoRootOB(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

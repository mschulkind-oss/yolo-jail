package openauthclient

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// THE LAUNCH'S CALLER TOKEN IS BOUND INTO THE MARKER CODEX HOLDS (callermarker.go,
// docs/plans/notch-convergence.md §2.3). The Codex launcher's token --codex-auth runs in the
// entry's environment, which carries the adapter's caller token; the adapter refuses a refresh
// whose marker does not carry it, so this writer is the one place Codex can get it from.
// Deleting the getenv(CallerTokenEnv) argument in Run fails this.
func TestRunTokenBindsTheAdaptersCallerTokenIntoTheCodexMarker(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), ".codex", "auth.json")
	endpoint := clientEndpoint(t, func(s *hostservice.Session) {
		_ = s.JSON(map[string]any{
			"access_token": "access-1", "id_token": "id-1", "refresh_token": "yolo-broker:3",
			"expires_at": int64(4_102_444_800_000),
		})
		s.Exit(0)
	})
	token := strings.Repeat("0f", 32)
	env := map[string]string{EndpointEnv: endpoint, CallerTokenEnv: token}
	var stdout, stderr bytes.Buffer
	if rc := Run([]string{"token", "--codex-auth", authPath}, func(k string) string { return env[k] },
		&stdout, &stderr); rc != 0 {
		t.Fatalf("rc=%d stderr=%q", rc, stderr.String())
	}
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	var auth struct {
		Tokens struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		t.Fatal(err)
	}
	if auth.Tokens.RefreshToken != "yolo-broker:3."+token {
		t.Fatalf("Codex's refresh marker = %q, want the broker's marker with the caller token bound",
			auth.Tokens.RefreshToken)
	}
	marker, got, ok := SplitCallerMarker(auth.Tokens.RefreshToken)
	if !ok || marker != "yolo-broker:3" || got != token {
		t.Errorf("SplitCallerMarker(%q) = %q, %q, %v", auth.Tokens.RefreshToken, marker, got, ok)
	}
	if strings.Contains(stdout.String(), token) {
		t.Errorf("stdout carries the caller token: %q", stdout.String())
	}
}

func TestSplitCallerMarkerReadsOnlyAWellFormedBinding(t *testing.T) {
	token := strings.Repeat("0f", 32)
	for _, presented := range []string{
		"yolo-broker:3", "yolo-broker:3." + token[:63], "yolo-broker:0." + token,
		"rt_real." + token, "yolo-broker:3." + strings.ToUpper(token), "",
	} {
		if _, _, ok := SplitCallerMarker(presented); ok {
			t.Errorf("SplitCallerMarker(%q) read a binding", presented)
		}
	}
	if BindCallerToken("yolo-broker:3", "") != "yolo-broker:3" {
		t.Error("an empty token must leave the marker plain")
	}
	if CallerTokenMatches(token, "") || !CallerTokenMatches(token, token) || CallerTokenMatches(token[:62]+"00", token) {
		t.Error("CallerTokenMatches is wrong")
	}
	if CallerTokenEnv != "YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN" {
		t.Errorf("CallerTokenEnv = %q, want the launcher's spelling for the loophole", CallerTokenEnv)
	}
}

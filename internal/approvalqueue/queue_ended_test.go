package approvalqueue

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/fakeyolo"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/model"
)

// startQueue runs the queue against a fake yolo and returns its origin and one-time code. stop
// cancels it and waits for run to return.
func startQueue(t *testing.T, fake string, args []string) (origin, code string, stop func()) {
	t.Helper()
	out, errb := newSyncBuf(), newSyncBuf()
	stdinR, _ := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Env{Yolo: fake, Args: args, Stdin: stdinR, Stdout: out, Stderr: errb, StdoutIsTerminal: true})
	}()
	stop = func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run returned %v", err)
			}
		case <-time.After(guard):
			t.Error("run did not return after cancel")
		}
	}
	m := regexp.MustCompile(`^Open: (http://127\.0\.0\.1:\d+)/#code=([A-Za-z0-9_-]{22})$`).FindStringSubmatch(waitLine(t, out, "Open: "))
	if m == nil {
		stop()
		t.Fatal("no link line")
	}
	return m[1], m[2], stop
}

func endedScript(t *testing.T, rec string) fakeyolo.Script {
	return fakeyolo.Script{Record: rec,
		JSON:      []fakeyolo.Run{{Stdout: td("approve-json-populated.json")}},
		JSONEnded: []fakeyolo.Run{{Stdout: td("approve-json-ended.json")}},
		Watch:     []fakeyolo.Run{{Block: true}}}
}

// BB-D113: `yolo approve --web --ended` passes --ended to every snapshot, and the ended
// request reaches the page's history masked, never among the pending ones. Without the flag
// the supervisor still runs plain "approve --json".
func TestWebEndedFlagReachesYoloAndShowsHistory(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	fake := fakeyolo.Install(t, endedScript(t, rec))
	origin, code, stop := startQueue(t, fake, []string{"--no-open", "--ended"})
	defer stop()
	cli := &http.Client{Timeout: guard}
	call := func(method, path, tok, body string) string {
		req, _ := http.NewRequest(method, origin+path, strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	var tok struct{ Token string }
	json.Unmarshal([]byte(call("POST", "/api/session", "", `{"code":"`+code+`"}`)), &tok)
	var pub model.Published
	if err := json.Unmarshal([]byte(call("GET", "/api/state", tok.Token, "")), &pub); err != nil {
		t.Fatal(err)
	}
	if len(pub.Pending) != 6 {
		t.Fatalf("pending = %d; an ended request must never be listed as pending", len(pub.Pending))
	}
	keys := map[string]bool{}
	for _, e := range pub.History.Entries {
		keys[e.Key] = true
	}
	for _, k := range []string{"r-ended0000000000000000000000000001", "g-ended0000000000000000000000000001", "w-ended00000000000000000000000001"} {
		if !keys[k] {
			t.Errorf("history lacks %s: %+v", k, pub.History.Entries)
		}
	}
	if !strings.Contains(pub.History.Note, "7 days") {
		t.Errorf("note lacks the store's horizon: %q", pub.History.Note)
	}
	for _, c := range fakeyolo.Calls(t, rec) {
		if c.Mode == "json" {
			t.Errorf("--ended queue ran a plain snapshot: %v", c.Args)
		}
	}
}

// Without --ended the queue is unchanged: plain snapshot, no ended records.
func TestWebWithoutEndedRunsPlainSnapshots(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	fake := fakeyolo.Install(t, endedScript(t, rec))
	_, _, stop := startQueue(t, fake, []string{"--no-open"})
	stop()
	for _, c := range fakeyolo.Calls(t, rec) {
		if c.Mode == "json-ended" {
			t.Errorf("plain queue passed --ended: %v", c.Args)
		}
	}
}

// TestServeForBrowserWitness serves the queue on the fixtures for a human or a browser driver
// (request-history-and-privacy.md, "Real-browser witness"). It is skipped unless
// YOLO_QUEUE_WITNESS_DIR names a directory; it writes the link there and runs until the file
// "stop" appears in it. Not a pass/fail test.
func TestServeForBrowserWitness(t *testing.T) {
	dir := os.Getenv("YOLO_QUEUE_WITNESS_DIR")
	if dir == "" {
		t.Skip("set YOLO_QUEUE_WITNESS_DIR to serve the queue for a browser check")
	}
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	fake := fakeyolo.Install(t, endedScript(t, rec))
	origin, code, stop := startQueue(t, fake, []string{"--no-open", "--ended", "--listen", "127.0.0.1:" + envOr("YOLO_QUEUE_WITNESS_PORT", "0")})
	defer stop()
	if err := os.WriteFile(filepath.Join(dir, "link"), []byte(origin+"/#code="+code+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond) // polling a stop file, not a pass condition
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

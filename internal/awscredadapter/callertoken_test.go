package awscredadapter

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// A DAEMON HANDED NO TOKEN BINDS NOTHING. It idles (so `restart: on-failure` does not
// crash-loop it) and never listens, because a listener would answer every process on the
// loopback. Deleting the token read in Main makes it bind the port instead.
func TestMainWithoutACallerTokenIdlesAndBindsNothing(t *testing.T) {
	t.Setenv(CallerTokenEnv, "")
	t.Setenv("HOME", t.TempDir()) // no shared file, so no scoped record either
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	idled := false
	prev := idleUntilStopped
	idleUntilStopped = func() {
		idled = true
		if conn, err := net.Dial("tcp", addr); err == nil {
			conn.Close()
			t.Errorf("an adapter with no caller token is listening on %s", addr)
		}
	}
	t.Cleanup(func() { idleUntilStopped = prev })
	if rc := Main([]string{"--listen", addr}); rc != 0 || !idled {
		t.Fatalf("Main = %d, idled = %v; want an idle that exits 0", rc, idled)
	}
}

// A SCOPED TOKEN IS READ FROM THE SHARED FILE'S RECORD (docs/reference/providers.md
// OQ-CN7 (c)). The launcher no longer exports the adapter's token into the environment every
// jail process inherits; it records it, unexported, in yolo-user-env.sh's channel section, and
// exports it only as the selecting agent's AWS_CONTAINER_AUTHORIZATION_TOKEN. The adapter's
// lookup must find the record when its environment carries no token, and must not find one
// outside the channel section. Deleting the lookup in Main makes the adapter idle instead.
func TestTheAdapterReadsItsScopedTokenFromTheSharedFilesRecord(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, ".config", "yolo-user-env.sh"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"HOME": home}
	getenv := func(k string) string { return env[k] }
	record := entrypoint.ScopedCallerTokenRecord(CallerTokenEnv, testToken)

	write(entrypoint.EntryChannelSectionHeader + "\n" + record)
	if tok, why := callerTokenFrom(callerTokenLookup(getenv)); tok != testToken || why != "" {
		t.Fatalf("the adapter found %q (%s), want the recorded token", tok, why)
	}
	write(record + entrypoint.EntryChannelSectionHeader + "\n")
	if tok, _ := callerTokenFrom(callerTokenLookup(getenv)); tok != "" {
		t.Errorf("a record above the channel section was read: %q", tok)
	}
	// A token in the environment (a launch that exported it) still answers first.
	env[CallerTokenEnv] = testToken
	if tok, _ := callerTokenFrom(callerTokenLookup(getenv)); tok != testToken {
		t.Errorf("the exported token was not used: %q", tok)
	}
}

// MAIN SERVES BEHIND THE RECORDED TOKEN: with no token in its environment and the scoped record
// in the jail home's shared file, the adapter binds and refuses a request without the token
// (401) while answering one with it (here 400 ServiceUnreachable, since the test publishes no
// host endpoint — so no credential service, and no AWS, is ever asked). Deleting the lookup in
// Main makes it idle and bind nothing, which fails the dial below.
func TestMainServesBehindTheScopedTokenRecord(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := entrypoint.EntryChannelSectionHeader + "\n" + entrypoint.ScopedCallerTokenRecord(CallerTokenEnv, testToken)
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-user-env.sh"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(CallerTokenEnv, "")
	t.Setenv(EndpointEnv, "")
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	prev := idleUntilStopped
	idleUntilStopped = func() { t.Error("the adapter idled although its scoped token was recorded") }
	t.Cleanup(func() { idleUntilStopped = prev })
	go Main([]string{"--listen", addr})

	status := func(authorization string) int {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+CredentialsPath, nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	deadline := time.Now().Add(5 * time.Second)
	for status("") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the adapter never listened behind its recorded token")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := status(""); got != http.StatusUnauthorized {
		t.Errorf("a request without the token = %d, want 401", got)
	}
	if got := status(testToken); got != http.StatusBadRequest {
		t.Errorf("a request with the token = %d, want past the caller check (400, no host endpoint)", got)
	}
}

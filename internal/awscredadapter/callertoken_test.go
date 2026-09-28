package awscredadapter

import (
	"net"
	"testing"
)

// A DAEMON HANDED NO TOKEN BINDS NOTHING. It idles (so `restart: on-failure` does not
// crash-loop it) and never listens, because a listener would answer every process on the
// loopback. Deleting the token read in Main makes it bind the port instead.
func TestMainWithoutACallerTokenIdlesAndBindsNothing(t *testing.T) {
	t.Setenv(CallerTokenEnv, "")
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

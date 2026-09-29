package integration

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMacosUserOpensTheCodexDoorwayOutsideTheSandbox is HS-D15, the doorway rule
// (docs/design/host-notch-services.md, ruled 2026-09-29; OQ-OA6 route (b)), on the hardware. A
// DOORWAY is that ruling's word for the thin adapter an agent's client talks to, which checks the
// launch's caller token and forwards to the credential service's host daemon. The sandbox shares
// the Mac's loopback, so a `"packs": ["codex"]` launch opens Codex's refresh doorway itself,
// OUTSIDE Seatbelt, as the launch's own listener, and stops it when the command exits.
//
// WHAT ONLY THIS TEST CAN SEE, none of it executed before: that a process the launch starts
// outside the sandbox, as the HOST user, is reachable from inside it at the address
// CODEX_REFRESH_TOKEN_URL_OVERRIDE names; that it refuses a refresh without the launch's caller
// token and admits one bound to it (the marker shape the Codex launcher writes); and that it is
// gone once the session ends. It does not run Codex: the probe speaks Codex's request shape with
// curl, and the broker behind the doorway needs no login for a refused or a forwarded request to
// be told apart from a missing listener.
func TestMacosUserOpensTheCodexDoorwayOutsideTheSandbox(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["codex"]}`)
	ws := macosUserWorkspace(t, `{}`)
	uid := sandboxUID(t)
	me := strconv.Itoa(os.Getuid())

	watch := watchForDoorway()
	t.Cleanup(func() { watch.stop() })
	body := func(marker string) string {
		return `'{"grant_type":"refresh_token","refresh_token":"'` + marker + `'"}'`
	}
	curl := func(marker string) string {
		return `curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d ` +
			body(marker) + ` "$CODEX_REFRESH_TOKEN_URL_OVERRIDE"`
	}
	r := macosUserRunProbe(t, "doorway", ws, strings.Join([]string{
		`echo "=== DOOR ==="`,
		`echo "URL=${CODEX_REFRESH_TOKEN_URL_OVERRIDE-UNSET}"`,
		`echo "NOTOKEN=$(` + curl("yolo-broker:1") + `)"`,
		`echo "TOKEN=$(` + curl(`yolo-broker:1.$YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN`) + `)"`,
		`echo "=== END ==="`,
	}, "\n"))
	saw := watch.stop()
	door := section(r.stdout, "=== DOOR ===", "=== END ===")
	diag := "\n--- probe:\n" + door + "\n--- launch stderr:\n" + r.stderr

	if !strings.Contains(r.combined(), `Opened the "openai-auth-broker" doorway (pack "openai-auth"`) {
		t.Errorf("the launch did not say it opened the doorway%s", diag)
	}
	if strings.Contains(r.combined(), "Started openai-auth-broker inside the sandbox") {
		t.Errorf("the guest ran the adapter's jail daemon too, so one address is served twice%s", diag)
	}
	got := map[string]string{}
	for _, line := range strings.Split(door, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}
	if !strings.HasPrefix(got["URL"], "http://127.0.0.1:") || strings.HasPrefix(got["URL"], "http://127.0.0.1:1460/") {
		t.Errorf("the sandbox's CODEX_REFRESH_TOKEN_URL_OVERRIDE is not a port picked for this launch%s", diag)
	}
	if got["NOTOKEN"] != "401" {
		t.Errorf("a refresh from inside the sandbox without the launch's token was not refused 401 "+
			"(000 means nothing answered at the address)%s", diag)
	}
	if got["TOKEN"] == "401" || got["TOKEN"] == "000" || got["TOKEN"] == "" {
		t.Errorf("a refresh carrying the launch's token did not get past the doorway%s", diag)
	}
	// Outside the sandbox: the host saw the doorway run as the host user, never the sandbox's.
	if saw == "" {
		t.Errorf("the host saw no `internal daemon openai-auth-adapter` during the session%s", diag)
	} else if f := strings.Fields(saw); len(f) < 1 || f[0] != me || f[0] == uid {
		t.Errorf("the doorway ran as uid %s, want the host user's %s (the sandbox account is %s): %s%s",
			f[0], me, uid, saw, diag)
	}
	// And it stops with the session.
	deadline := time.Now().Add(10 * time.Second)
	for {
		left, _ := exec.Command("pgrep", "-f", "internal daemon openai-auth-adapter").Output()
		if strings.TrimSpace(string(left)) == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("the doorway survives the session (pids %s)", strings.TrimSpace(string(left)))
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// doorwayWatch polls the host's process table for the doorway during a session.
type doorwayWatch struct {
	quit chan struct{}
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	saw  string
}

func watchForDoorway() *doorwayWatch {
	w := &doorwayWatch{quit: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			for _, line := range strings.Split(hostProcessListing(), "\n") {
				if strings.Contains(line, "internal daemon openai-auth-adapter --listen") &&
					!strings.Contains(line, "pgrep") {
					w.mu.Lock()
					w.saw = strings.TrimSpace(line)
					w.mu.Unlock()
					return
				}
			}
			select {
			case <-w.quit:
				return
			case <-tick.C:
			}
		}
	}()
	return w
}

// stop ends the poll and returns the doorway's listing line ("" for none).
func (w *doorwayWatch) stop() string {
	w.once.Do(func() { close(w.quit) })
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saw
}

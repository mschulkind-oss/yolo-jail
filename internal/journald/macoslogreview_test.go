package journald

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ndjsonTime renders t the way `log --style ndjson` spells an entry's timestamp.
func ndjsonTime(t time.Time) string { return t.Format("2006-01-02 15:04:05.000000-0700") }

// UNDER `show` A LINE WITHOUT userID IS DROPPED, whoever owns its pid now. `show` reads history
// (`--last 7d`), and a pid's CURRENT owner says nothing about who held it when the entry was
// logged: a host-user entry whose pid a sandbox process holds today would otherwise pass.
func TestShowDropsALineWithoutUserIDEvenWhenThePidIsTheSandboxs(t *testing.T) {
	asked := false
	keep := macosLogKeep(401, false, func(int) (uint32, time.Time, bool) {
		asked = true
		return 401, time.Unix(0, 0), true
	})
	line := `{"processID": 100, "timestamp": "` + ndjsonTime(time.Now()) + `", "eventMessage": "host"}`
	if keep([]byte(line)) {
		t.Errorf("show kept a line with no userID on its pid's current owner: %s", line)
	}
	if asked {
		t.Error("show asked the kernel for a pid's owner; history must never be judged by it")
	}
	if !keep([]byte(`{"userID": 401, "processID": 100}`)) {
		t.Error("show dropped a line whose own userID is the sandbox account's")
	}
}

// UNDER `stream` THE LIVE-OWNER FALLBACK HOLDS ONLY FOR A PROCESS THAT STARTED BY THE ENTRY'S
// TIME: one that started later is a recycled pid, and the entry is its predecessor's.
func TestStreamFallbackRequiresTheProcessToPredateTheEntry(t *testing.T) {
	started := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	keep := macosLogKeep(401, true, func(pid int) (uint32, time.Time, bool) {
		return 401, started, pid == 100
	})
	for _, tc := range []struct {
		line string
		want bool
	}{
		{`{"processID": 100, "timestamp": "` + ndjsonTime(started.Add(time.Second)) + `"}`, true},
		{`{"processID": 101, "timestamp": "` + ndjsonTime(started.Add(time.Second)) + `"}`, false},
		{`{"processID": 100, "timestamp": "` + ndjsonTime(started.Add(-time.Second)) + `"}`, false},
		{`{"processID": 100, "timestamp": "yesterday"}`, false},
		{`{"processID": 100}`, false},
	} {
		if got := keep([]byte(tc.line)); got != tc.want {
			t.Errorf("stream keep(%s) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// Only `stream` is live; `show`, the default included, is history.
func TestOnlyStreamIsALiveRead(t *testing.T) {
	for args, want := range map[string]bool{"": false, "show --last 7d": false, "--last 1m": false,
		"stream": true, "stream --level debug": true} {
		if p := PlanMacosLog(strings.Fields(args), ModeUser); p.Live != want {
			t.Errorf("%q: Live = %v, want %v", args, p.Live, want)
		}
	}
}

// AN ABANDONED USER-SCOPE STREAM STOPS ITS `log`. The filter may send nothing for minutes, so a
// failed write cannot be what notices the client left: the bridge watches the connection itself.
func TestAnAbandonedUserScopeStreamTerminatesItsLog(t *testing.T) {
	saved := macosLogBin
	t.Cleanup(func() { macosLogBin = saved })
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	macosLogBin = filepath.Join(dir, "log")
	script := "#!/bin/sh\necho $$ > " + pidFile + "\nexec sleep 300\n"
	if err := os.WriteFile(macosLogBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleMacosLogConn(server, MacosLogConfig{Bin: macosLogBin, Mode: ModeUser, SandboxUID: 401,
			Owner: func(int) (uint32, time.Time, bool) { return 0, time.Time{}, false }})
	}()
	if _, err := client.Write([]byte(`{"args":["stream"]}` + "\n")); err != nil {
		t.Fatal(err)
	}
	var pid int
	for deadline := time.Now().Add(10 * time.Second); pid == 0 && time.Now().Before(deadline); {
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the fake log never started")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the bridge kept serving a stream whose client had gone")
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("the abandoned stream's log (pid %d) is still running", pid)
	}
}

// THE CALL SITE: MacosLogMain serves with the real `log`, the kernel's process owner and the
// account lookup — not a test seam left in place.
func TestMacosLogMainWiresTheRealOwnerAndAccount(t *testing.T) {
	saved := lookupSandboxUID
	t.Cleanup(func() { lookupSandboxUID = saved })
	lookupSandboxUID = func() (uint32, error) { return 777, nil }
	cfg := macosLogMainConfig(ModeFull)
	if cfg.Bin != "/usr/bin/log" || cfg.Mode != ModeFull || cfg.SandboxUID != 777 || cfg.SandboxUIDErr != nil {
		t.Errorf("config = %+v, want /usr/bin/log, full, uid 777 from lookupSandboxUID", cfg)
	}
	if reflect.ValueOf(cfg.Owner).Pointer() != reflect.ValueOf(processOwner).Pointer() {
		t.Error("MacosLogMain does not attribute entries with processOwner")
	}
}

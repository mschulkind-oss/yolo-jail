package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestTraceOfflineRegistered(t *testing.T) {
	if _, ok := registry["trace"]; !ok {
		t.Fatal("trace is absent from dispatch")
	}
}

func TestTraceOfflineEmpty(t *testing.T) {
	var out, errs bytes.Buffer
	traceMain([]string{"trace", "show"}, traceEnv{root: t.TempDir(), workspaceCode: "abcd1234"}, &out, &errs)
	text := out.String() + errs.String()
	if !strings.Contains(text, "perf_logging") || !strings.Contains(text, "yolo config set") {
		t.Fatalf("empty trace advice lacks recording next step: %q", text)
	}
}

func TestTraceOfflineSelection(t *testing.T) {
	const id = "11111111111111111111111111111111"
	root := t.TempDir()
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	meta := `{"trace":"11111111111111111111111111111111","kind":"launch","start":1795111105000000000,"jail":"yolo-ws-abcd1234","workspace":"abcd1234","version":"test","commit":"test"}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}
	lane := `{"k":"lane","name":"launcher","pid":7,"trace":"11111111111111111111111111111111"}
{"t":1795111105000000000,"k":"start","name":"launch","span":"1111111111111111"}
{"t":1795111105004000000,"k":"end","name":"launch","span":"1111111111111111","dur":4000000}
`
	if err := os.WriteFile(filepath.Join(dir, "launcher-7.jsonl"), []byte(lane), 0644); err != nil {
		t.Fatal(err)
	}
	env := traceEnv{root: root, workspaceCode: "abcd1234"}
	for _, args := range [][]string{{"trace"}, {"trace", "show"}, {"trace", "show", id}} {
		var out, errs bytes.Buffer
		if rc := traceMain(args, env, &out, &errs); rc != 0 || !strings.Contains(out.String(), id) {
			t.Fatalf("%v: rc=%d out=%q err=%q", args, rc, &out, &errs)
		}
	}
	var out, errs bytes.Buffer
	if rc := traceMain([]string{"trace", "show", "22222222222222222222222222222222"}, env, &out, &errs); rc == 0 ||
		!strings.Contains(errs.String(), "yolo trace") {
		t.Fatalf("unknown id: rc=%d err=%q", rc, &errs)
	}
}

func traceOfflineFixture(t *testing.T, root, id, kind, workspace string, start int64, events string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	meta, err := json.Marshal(map[string]any{
		"trace": id, "kind": kind, "start": start, "workspace": workspace,
		"jail": "yolo-ws-" + workspace, "version": "test", "commit": "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0644); err != nil {
		t.Fatal(err)
	}
	if events == "" {
		events = fmt.Sprintf("{\"t\":%d,\"k\":\"record\",\"name\":\"work\",\"span\":\"1111111111111111\",\"dur\":4000000}\n", start+4000000)
	}
	lane := fmt.Sprintf("{\"k\":\"lane\",\"name\":\"launcher\",\"pid\":7,\"trace\":%q}\n", id) + events
	if err := os.WriteFile(filepath.Join(dir, "launcher-7.jsonl"), []byte(lane), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func traceOfflineCall(args []string, env traceEnv) (int, string, string) {
	var out, errs bytes.Buffer
	rc := traceMain(args, env, &out, &errs)
	return rc, out.String(), errs.String()
}

func TestTraceOfflineKindsAndWorkspaces(t *testing.T) {
	root := t.TempDir()
	const old = "11111111111111111111111111111111"
	const latest = "22222222222222222222222222222222"
	const prep = "33333333333333333333333333333333"
	const other = "44444444444444444444444444444444"
	for i, row := range []struct{ id, kind, ws string }{
		{old, "launch", "abcd1234"}, {latest, "launch", "abcd1234"},
		{prep, "preparation", "abcd1234"}, {other, "launch", "eeeeffff"},
	} {
		traceOfflineFixture(t, root, row.id, row.kind, row.ws, 1795111105000000000+int64(i)*1000000000, "")
	}
	env := traceEnv{root: root, workspaceCode: "abcd1234"}
	for _, args := range [][]string{{"trace"}, {"trace", "list"}} {
		rc, out, errs := traceOfflineCall(args, env)
		if rc != 0 || errs != "" {
			t.Fatalf("list: rc=%d out=%q errs=%q", rc, out, errs)
		}
		last := -1
		for _, id := range []string{old, latest, prep, other} {
			pos := strings.Index(out, id)
			if pos <= last {
				t.Fatalf("not machine-wide/oldest first: %q", out)
			}
			last = pos
		}
		if !strings.Contains(out, "preparation") || !strings.Contains(out, "eeeeffff") {
			t.Fatal(out)
		}
	}
	rc, out, errs := traceOfflineCall([]string{"trace", "show"}, env)
	if rc != 0 || !strings.Contains(out, "Trace "+latest) || strings.Contains(out, "Trace "+other) || strings.Contains(out, "Trace "+prep) {
		t.Fatalf("latest launch: rc=%d out=%q errs=%q", rc, out, errs)
	}
	rc, out, errs = traceOfflineCall([]string{"trace", "show", prep}, env)
	if rc != 0 || !strings.Contains(out, "Trace "+prep+" (preparation)") {
		t.Fatalf("explicit preparation: rc=%d out=%q errs=%q", rc, out, errs)
	}
	for _, ws := range []string{"", "absent"} {
		env.workspaceCode = ws
		rc, out, errs = traceOfflineCall([]string{"trace", "show"}, env)
		if rc == 0 || strings.Contains(out, "Trace ") || !strings.Contains(errs, "`yolo trace`") {
			t.Fatalf("no workspace: rc=%d out=%q errs=%q", rc, out, errs)
		}
	}
}

func TestTraceOfflineLatestMetadataStart(t *testing.T) {
	const older = "11111111111111111111111111111111"
	const newer = "22222222222222222222222222222222"
	const base int64 = 1795111105000000000
	for _, scenario := range []string{"metadata only", "event order disagreement", "equal start ID tie"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			oldStart, newStart := base, base+1000000000
			oldEvents := fmt.Sprintf("{\"t\":%d,\"k\":\"record\",\"name\":\"old\",\"span\":\"1111111111111111\",\"dur\":4000000}\n", base+2000000000)
			if scenario == "equal start ID tie" {
				newStart = oldStart
			}
			traceOfflineFixture(t, root, older, "launch", "abcd1234", oldStart, oldEvents)
			newEvents := fmt.Sprintf("{\"t\":%d,\"k\":\"record\",\"name\":\"new\",\"span\":\"1111111111111111\",\"dur\":4000000}\n", base+4000000)
			dir := traceOfflineFixture(t, root, newer, "launch", "abcd1234", newStart, newEvents)
			if scenario == "metadata only" {
				if err := os.Remove(filepath.Join(dir, "launcher-7.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			// A trace without metadata cannot identify its workspace or kind.
			unidentified := traceOfflineFixture(t, root, strings.Repeat("3", 32), "launch", "abcd1234", base+3000000000, "")
			if err := os.Remove(filepath.Join(unidentified, "meta.json")); err != nil {
				t.Fatal(err)
			}
			rc, out, errs := traceOfflineCall([]string{"trace", "show"}, traceEnv{root: root, workspaceCode: "abcd1234"})
			if rc != 0 || !strings.Contains(out, "Trace "+newer+" (launch)") {
				t.Fatalf("latest metadata start: rc=%d out=%q errs=%q", rc, out, errs)
			}
		})
	}
}

func TestTraceOfflineWrapperHostIdentity(t *testing.T) {
	const hostID = "11111111111111111111111111111111"
	const guestID = "22222222222222222222222222222222"
	const base int64 = 1795111105000000000
	for _, scenario := range []string{"distinct host label", "host label is guest symlink", "unmatched absolute origin", "missing origin", "relative origin"} {
		t.Run(scenario, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			guest := filepath.Join(home, "guest-workspace")
			if err := os.Mkdir(guest, 0755); err != nil {
				t.Fatal(err)
			}
			hostLabel := "/host-origin/trace-project"
			switch scenario {
			case "host label is guest symlink":
				hostLabel = filepath.Join(home, "host-label")
				if err := os.Symlink(guest, hostLabel); err != nil {
					t.Fatal(err)
				}
			case "missing origin":
				hostLabel = ""
			case "relative origin":
				hostLabel = "relative/host-project"
			}
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "trace-wrapper-fixture")
			t.Setenv("YOLO_WORKSPACE", guest)
			t.Setenv("YOLO_HOST_DIR", hostLabel)
			root := filepath.Join(paths.LogsDir(), "traces")
			if err := os.MkdirAll(root, 0755); err != nil {
				t.Fatal(err)
			}
			hostCode := paths.JailShortHash(runtime.FromResolved(hostLabel))
			if scenario == "unmatched absolute origin" {
				hostCode = paths.JailShortHash(runtime.FromResolved("/host-origin/canonical-project"))
			}
			guestCode := paths.JailShortHash(runtime.FromWorkspace(guest))
			if hostCode == guestCode {
				t.Fatal("fixture does not distinguish host label from guest identity")
			}
			traceOfflineFixture(t, root, hostID, "launch", hostCode, base, "")
			traceOfflineFixture(t, root, guestID, "launch", guestCode, base+1000000000, "")
			// Exercise the actual wrapper, not an injected traceEnv.
			rc, out, errs := captureStd(t, func() int { return runTrace([]string{"trace", "show"}) })
			if scenario == "missing origin" || scenario == "relative origin" || scenario == "unmatched absolute origin" {
				if rc != 1 || strings.Contains(out, "Trace ") || !strings.Contains(errs, "`yolo trace`") || !strings.Contains(errs, "on the host") {
					t.Fatalf("unknown origin: rc=%d out=%q errs=%q", rc, out, errs)
				}
			} else if rc != 0 || !strings.Contains(out, "Trace "+hostID+" (launch)") || strings.Contains(out, "Trace "+guestID) {
				t.Fatalf("host-origin selection: rc=%d out=%q errs=%q", rc, out, errs)
			}
			// Unknown origin must still permit listing and explicit selection.
			rc, out, errs = captureStd(t, func() int { return runTrace([]string{"trace"}) })
			if rc != 0 || !strings.Contains(out, hostID) || !strings.Contains(out, guestID) {
				t.Fatalf("wrapper list: rc=%d out=%q errs=%q", rc, out, errs)
			}
			rc, out, errs = captureStd(t, func() int { return runTrace([]string{"trace", "show", hostID}) })
			if rc != 0 || !strings.Contains(out, "Trace "+hostID) {
				t.Fatalf("explicit selection: rc=%d out=%q errs=%q", rc, out, errs)
			}
		})
	}
}

func TestTraceOfflineTopFifteen(t *testing.T) {
	root := t.TempDir()
	const id = "11111111111111111111111111111111"
	var events strings.Builder
	timestamp := int64(1795111105000000000)
	for i := 1; i <= 18; i++ {
		duration := int64(i) * 1000000
		timestamp += duration
		fmt.Fprintf(&events, "{\"t\":%d,\"k\":\"record\",\"name\":\"work%02d\",\"span\":\"%016x\",\"dur\":%d}\n", timestamp, i, i, duration)
	}
	traceOfflineFixture(t, root, id, "launch", "abcd1234", 1795111105000000000, events.String())
	rc, out, errs := traceOfflineCall([]string{"trace", "show", id}, traceEnv{root: root})
	if rc != 0 || errs != "" || strings.Count(out, "  lane launcher") != 15 || !strings.Contains(out, "18ms  work18") || !strings.Contains(out, "4ms  work04") || strings.Contains(out, "work03") {
		t.Fatalf("top fifteen: rc=%d out=%q errs=%q", rc, out, errs)
	}
}

func TestTraceOfflineRootsAndGuidance(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "not-a-directory")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	for _, jail := range []bool{false, true} {
		for _, root := range []string{filepath.Join(base, "absent"), t.TempDir(), file} {
			rc, out, errs := traceOfflineCall([]string{"trace"}, traceEnv{root: root, inJail: jail})
			text := out + errs
			if root == file {
				if rc != 1 || !strings.Contains(errs, "cannot read") || !strings.Contains(errs, "host") {
					t.Fatalf("unreadable: rc=%d text=%q", rc, text)
				}
			} else if rc != 0 || !strings.Contains(text, "yolo init-user-config") || !strings.Contains(text, "yolo config set perf_logging true --user") || !strings.Contains(text, "not yet wired") {
				t.Fatalf("empty: rc=%d text=%q", rc, text)
			}
			if jail && !strings.Contains(text, "on the host") {
				t.Fatalf("jail lacks host guidance: %q", text)
			}
			if root != file {
				for _, args := range [][]string{{"trace", "show"}, {"trace", "show", strings.Repeat("1", 32)}} {
					rc, out, errs := traceOfflineCall(args, traceEnv{root: root, inJail: jail, workspaceCode: "abcd1234"})
					if rc != 1 || !strings.Contains(errs, "yolo trace") || !strings.Contains(out, "perf_logging true --user") || (jail && !strings.Contains(errs, "on the host")) {
						t.Fatalf("missing show: rc=%d out=%q errs=%q", rc, out, errs)
					}
				}
			}
		}
	}
}

func TestTraceOfflineInvalidIDsAndFlags(t *testing.T) {
	// A nonexistent root makes this a witness that invalid input is refused before reading.
	env := traceEnv{root: filepath.Join(t.TempDir(), "absent")}
	for _, id := range []string{"../11111111111111111111111111111111", "/11111111111111111111111111111111", "ABCDEF11111111111111111111111111", "short", "--format"} {
		rc, _, errs := traceOfflineCall([]string{"trace", "show", id}, env)
		if rc != 2 || !strings.Contains(errs, "lowercase hex") || !strings.Contains(errs, "yolo trace") {
			t.Fatalf("%q rc=%d errs=%q", id, rc, errs)
		}
	}
	for _, args := range [][]string{{"trace", "list", "extra"}, {"trace", "show", strings.Repeat("1", 32), "extra"}, {"trace", "export", "-o", "--help"}, {"trace", "-o", "--help"}} {
		rc, out, errs := traceOfflineCall(args, env)
		if rc != 2 || strings.Contains(out, "Usage:") || !strings.Contains(errs, "yolo trace") {
			t.Fatalf("%v rc=%d out=%q errs=%q", args, rc, out, errs)
		}
	}
}

func TestTraceOfflineHelpRegistries(t *testing.T) {
	if Subcommand([]string{"trace", "show"}) != "trace" || !IsNative("trace") || !strings.Contains(usageText(), "trace") {
		t.Fatal("actual dispatch/top help does not expose trace")
	}
	for _, flag := range []string{"--help", "-h", "help"} {
		// Exercise the real registered handler; help must not need root/workspace discovery.
		if rc := registry["trace"]([]string{"trace", flag}); rc != 0 {
			t.Fatalf("registry help %s: %d", flag, rc)
		}
		rc, out, errs := traceOfflineCall([]string{"trace", flag}, traceEnv{})
		if rc != 0 || errs != "" || !strings.Contains(out, "offline") || !strings.Contains(out, "not implemented") || !strings.Contains(out, "user-only") {
			t.Fatalf("help: rc=%d out=%q errs=%q", rc, out, errs)
		}
	}
	if subcommandHelpRequested("trace", []string{"trace", "export", "-o", "--help"}) {
		t.Fatal("an unimplemented -o value was treated as help")
	}
}

func TestTraceOfflineSummaryAndDiagnostics(t *testing.T) {
	root := t.TempDir()
	const id = "11111111111111111111111111111111"
	const overlap = "22222222222222222222222222222222"
	const base int64 = 1795111105000000000
	events := fmt.Sprintf(`{"t":%d,"k":"start","name":"launch","span":"1111111111111111"}
{"t":%d,"k":"start","name":"child","span":"2222222222222222"}
{"t":%d,"k":"end","name":"child","span":"2222222222222222","dur":100000000}
{"t":%d,"k":"mark","name":"last"}
not-json
`, base, base+200000000, base+300000000, base+1000000000)
	dir := traceOfflineFixture(t, root, id, "launch", "abcd1234", base, events)
	traceOfflineFixture(t, root, overlap, "preparation", "other", base+400000000, "")
	if err := os.WriteFile(filepath.Join(dir, "broken.jsonl"), []byte("not a header\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Duplicate a span in another lane: U2 reports an anomaly, not a silent omission.
	duplicate := fmt.Sprintf("{\"k\":\"lane\",\"name\":\"worker\",\"pid\":8,\"trace\":%q}\n{\"t\":%d,\"k\":\"record\",\"name\":\"duplicate\",\"span\":\"2222222222222222\",\"dur\":100000000}\n", id, base+300000000)
	if err := os.WriteFile(filepath.Join(dir, "worker-8.jsonl"), []byte(duplicate), 0644); err != nil {
		t.Fatal(err)
	}
	// A trace-id entry that is not a directory is a ReadRoot skipped diagnostic.
	if err := os.WriteFile(filepath.Join(root, strings.Repeat("3", 32)), nil, 0644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := traceOfflineCall([]string{"trace", "show", id}, traceEnv{root: root})
	if rc != 0 {
		t.Fatalf("summary: rc=%d errs=%q", rc, errs)
	}
	for _, want := range []string{"Startup window:", "(1s), incomplete", "Lane shares (busy / own):", "launcher pid 7: 1s / 1s", "900ms  launch", "100ms  child", "Gaps over 100 ms:", "200ms within launch", "700ms within launch", "Unfinished spans: 1", "Missing jail half", "Overlapping traces: 1", overlap + " (preparation)"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q: %s", want, out)
		}
	}
	for _, want := range []string{"Skipped", "broken.jsonl", "not a directory", "anomaly", "used in both", "1 dropped rows"} {
		if !strings.Contains(errs, want) {
			t.Errorf("diagnostics lack %q: %s", want, errs)
		}
	}
	// A collected jail marker completes the window without changing host start.
	jail := fmt.Sprintf("{\"t\":%d,\"k\":\"mark\",\"name\":\"session.exec\"}\n", base+500000000)
	if err := os.WriteFile(filepath.Join(dir, "jail.jsonl"), []byte(jail), 0644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs = traceOfflineCall([]string{"trace", "show", id}, traceEnv{root: root})
	if rc != 0 || !strings.Contains(out, "(500ms), complete (session.exec)") || strings.Contains(out, "Missing jail half") || !strings.Contains(out, "Unfinished spans: 1") {
		t.Fatalf("complete window: rc=%d out=%q errs=%q", rc, out, errs)
	}
}

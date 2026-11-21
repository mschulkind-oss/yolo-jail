package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/tracefile"
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
	for _, args := range [][]string{{"trace", "list", "extra"}, {"trace", "show", strings.Repeat("1", 32), "extra"}, {"trace", "export", "-o", "--help", "extra"}, {"trace", "-o", "--help"}} {
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
		if rc != 0 || errs != "" || !strings.Contains(out, "offline") || !strings.Contains(out, "export") || !strings.Contains(out, "user-only") || !strings.Contains(out, "not yet wired") {
			t.Fatalf("help: rc=%d out=%q errs=%q", rc, out, errs)
		}
	}
	if subcommandHelpRequested("trace", []string{"trace", "export", "-o", "--help"}) ||
		traceAnswerHelp([]string{"trace", "export", "--format", "--help"}, &bytes.Buffer{}) {
		t.Fatal("a trace export flag value was treated as help")
	}
	if !traceAnswerHelp([]string{"trace", "export", "--format", "chrome", "--help"}, &bytes.Buffer{}) {
		t.Fatal("trace export help was not recognized after a format value")
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

type traceExportFailWriter struct{}

func (traceExportFailWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("fixture output failure")
}

func traceExportEvents(name string, start int64) string {
	const span = "1111111111111111"
	return fmt.Sprintf("{\"t\":%d,\"k\":\"start\",\"name\":%q,\"span\":%q}\n{\"t\":%d,\"k\":\"end\",\"name\":%q,\"span\":%q,\"dur\":4000000}\n", start, name, span, start+4000000, name, span)
}

func TestTraceOfflineExportSelectionAndFormats(t *testing.T) {
	root := t.TempDir()
	const tiedLower = "11111111111111111111111111111111"
	const tiedLatest = "22222222222222222222222222222222"
	const preparation = "33333333333333333333333333333333"
	const otherWorkspace = "44444444444444444444444444444444"
	const base int64 = 1795111105000000000
	traceOfflineFixture(t, root, tiedLower, "launch", "abcd1234", base, traceExportEvents("tie-lower", base))
	traceOfflineFixture(t, root, tiedLatest, "launch", "abcd1234", base, traceExportEvents("selected-launch", base))
	traceOfflineFixture(t, root, preparation, "preparation", "abcd1234", base+9000000000, traceExportEvents("selected-preparation", base))
	traceOfflineFixture(t, root, otherWorkspace, "launch", "eeeeffff", base+10000000000, traceExportEvents("other-workspace", base))
	env := traceEnv{root: root, workspaceCode: "abcd1234"}

	var chromeOut, diagnostics bytes.Buffer
	if rc := traceMain([]string{"trace", "export"}, env, &chromeOut, &diagnostics); rc != 0 || diagnostics.Len() != 0 {
		t.Fatalf("default Chrome export: rc=%d stderr=%q", rc, diagnostics.String())
	}
	var chrome struct {
		Events []struct {
			Name  string         `json:"name"`
			Phase string         `json:"ph"`
			Args  map[string]any `json:"args"`
		} `json:"traceEvents"`
	}
	if err := json.Unmarshal(chromeOut.Bytes(), &chrome); err != nil {
		t.Fatalf("decode Chrome export: %v: %s", err, chromeOut.String())
	}
	foundChromeSpan := false
	for _, event := range chrome.Events {
		if event.Name == "selected-launch" && event.Phase == "X" && event.Args["yolo.trace_id"] == tiedLatest {
			foundChromeSpan = true
		}
		if event.Name == "selected-preparation" || event.Name == "other-workspace" || event.Name == "tie-lower" {
			t.Errorf("default selection exported %q", event.Name)
		}
	}
	if !foundChromeSpan {
		t.Fatalf("default export did not contain selected launch: %s", chromeOut.String())
	}

	var otlpOut, otlpErr bytes.Buffer
	args := []string{"trace", "export", "--format", "otlp", preparation}
	if rc := traceMain(args, env, &otlpOut, &otlpErr); rc != 0 || otlpErr.Len() != 0 {
		t.Fatalf("explicit OTLP export: rc=%d stderr=%q", rc, otlpErr.String())
	}
	var otlp struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []struct {
					TraceID string `json:"traceId"`
					Name    string `json:"name"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(otlpOut.Bytes(), &otlp); err != nil {
		t.Fatalf("decode OTLP export: %v: %s", err, otlpOut.String())
	}
	foundOTLPSpan := false
	for _, resource := range otlp.ResourceSpans {
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				if span.Name == "selected-preparation" && span.TraceID == preparation {
					foundOTLPSpan = true
				}
				if span.Name == "selected-launch" || span.Name == "other-workspace" {
					t.Errorf("explicit selection exported %q", span.Name)
				}
			}
		}
	}
	if !foundOTLPSpan {
		t.Fatalf("explicit export did not contain requested preparation trace: %s", otlpOut.String())
	}
}

func TestTraceOfflineExportArgumentsFailBeforeOutput(t *testing.T) {
	root := t.TempDir()
	const id = "11111111111111111111111111111111"
	traceOfflineFixture(t, root, id, "launch", "abcd1234", 1795111105000000000, "")
	target := filepath.Join(t.TempDir(), "prior.json")
	const prior = "prior output must survive\n"
	if err := os.WriteFile(target, []byte(prior), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"trace", "export", "--format"},
		{"trace", "export", "--format", "--help"},
		{"trace", "export", "--format", "yaml"},
		{"trace", "export", "--format", "chrome", "--format", "otlp"},
		{"trace", "export", "short"},
		{"trace", "export", "-o"},
		{"trace", "export", "-o", ""},
		{"trace", "export", "-o", target, "-o", target},
		{"trace", "export", id, "extra"},
		{"trace", "export", id, "extra", "-o", target},
		{"trace", "export", "--unknown"},
	} {
		var out, errs bytes.Buffer
		if rc := traceMain(args, traceEnv{root: root, workspaceCode: "abcd1234"}, &out, &errs); rc != 2 || out.Len() != 0 || !strings.Contains(errs.String(), "yolo trace export") {
			t.Errorf("invalid args %v: rc=%d stdout=%q stderr=%q", args, rc, out.String(), errs.String())
		}
		if b, err := os.ReadFile(target); err != nil || string(b) != prior {
			t.Fatalf("invalid args %v changed prior file: %q, %v", args, b, err)
		}
	}
}

func TestTraceOfflineExportAtomicFiles(t *testing.T) {
	root := t.TempDir()
	const id = "11111111111111111111111111111111"
	const base int64 = 1795111105000000000
	traceOfflineFixture(t, root, id, "launch", "abcd1234", base, traceExportEvents("work", base))
	parent := t.TempDir()
	target := filepath.Join(parent, "trace.json")
	const prior = "old complete trace\n"
	if err := os.WriteFile(target, []byte(prior), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if rc := traceMain([]string{"trace", "export", id, "-o", target}, traceEnv{root: root}, &out, &errs); rc != 0 || out.Len() != 0 || errs.Len() != 0 {
		t.Fatalf("file export: rc=%d stdout=%q stderr=%q", rc, out.String(), errs.String())
	}
	written, err := os.ReadFile(target)
	if err != nil || !json.Valid(written) || string(written) == prior {
		t.Fatalf("file export not published: %q, %v", written, err)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 1 {
		t.Fatalf("temporary artifact survived publication: entries=%v err=%v", entries, err)
	}

	// OTLP rejects duplicate span ids; failure after temporary creation must preserve
	// the previous complete file and remove the unpublished temporary.
	const duplicate = "22222222222222222222222222222222"
	dupDir := filepath.Join(root, duplicate)
	if err := os.Mkdir(dupDir, 0755); err != nil {
		t.Fatal(err)
	}
	meta := fmt.Sprintf(`{"trace":%q,"kind":"launch","start":%d,"jail":"yolo-ws-abcd1234","workspace":"abcd1234","version":"test","commit":"test"}`, duplicate, base)
	if err := os.WriteFile(filepath.Join(dupDir, "meta.json"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}
	for i, lane := range []string{"a", "b"} {
		contents := fmt.Sprintf("{\"k\":\"lane\",\"name\":%q,\"pid\":%d,\"trace\":%q}\n{\"t\":%d,\"k\":\"record\",\"name\":\"duplicate\",\"span\":\"1111111111111111\",\"dur\":4000000}\n", lane, i+1, duplicate, base)
		if err := os.WriteFile(filepath.Join(dupDir, lane+".jsonl"), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(target, []byte(prior), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errs.Reset()
	if rc := traceMain([]string{"trace", "export", duplicate, "--format", "otlp", "-o", target}, traceEnv{root: root}, &out, &errs); rc == 0 || out.Len() != 0 {
		t.Fatalf("expected OTLP publication failure: rc=%d out=%q err=%q", rc, out.String(), errs.String())
	}
	if !strings.Contains(errs.String(), "Next:") ||
		!strings.Contains(errs.String(), "yolo trace show "+duplicate) ||
		!strings.Contains(errs.String(), "yolo trace export "+duplicate) ||
		strings.Contains(errs.String(), target) {
		t.Fatalf("duplicate-span failure has no safe actionable next step: %q", errs.String())
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != prior {
		t.Fatalf("failed export damaged prior target: %q, %v", b, err)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 1 {
		t.Fatalf("failed temporary survived: entries=%v err=%v", entries, err)
	}
}

func TestTraceOfflineExportRejectsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	const id = "11111111111111111111111111111111"
	traceOfflineFixture(t, root, id, "launch", "abcd1234", 1795111105000000000, "")
	base := t.TempDir()
	missingParentTarget := filepath.Join(base, "missing", "trace.json")
	if rc, out, errs := traceOfflineCall([]string{"trace", "export", id, "-o", missingParentTarget}, traceEnv{root: root}); rc == 0 || out != "" || !strings.Contains(errs, "cannot") || strings.Contains(errs, missingParentTarget) {
		t.Fatalf("missing parent: rc=%d out=%q err=%q", rc, out, errs)
	}
	if _, err := os.Lstat(filepath.Dir(missingParentTarget)); !os.IsNotExist(err) {
		t.Fatalf("export created missing parent: %v", err)
	}

	victim := filepath.Join(base, "victim")
	const sentinel = "symlink target stays untouched\n"
	if err := os.WriteFile(victim, []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link.json")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if rc, out, errs := traceOfflineCall([]string{"trace", "export", id, "-o", link}, traceEnv{root: root}); rc == 0 || out != "" || !strings.Contains(errs, "symlink") || strings.Contains(errs, link) {
		t.Fatalf("symlink target: rc=%d out=%q err=%q", rc, out, errs)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was changed: %v, %v", fi, err)
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != sentinel {
		t.Fatalf("symlink referent changed: %q, %v", b, err)
	}

	directory := filepath.Join(base, "directory")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if rc, out, errs := traceOfflineCall([]string{"trace", "export", id, "-o", directory}, traceEnv{root: root}); rc == 0 || out != "" || !strings.Contains(errs, "regular") || strings.Contains(errs, directory) {
		t.Fatalf("directory target: rc=%d out=%q err=%q", rc, out, errs)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("directory target changed: %v, %v", entries, err)
	}

	literalDash := filepath.Join(base, "-")
	if rc, out, errs := traceOfflineCall([]string{"trace", "export", id, "-o", literalDash}, traceEnv{root: root}); rc != 0 || out != "" || errs != "" {
		t.Fatalf("literal dash filename: rc=%d out=%q err=%q", rc, out, errs)
	}
	if b, err := os.ReadFile(literalDash); err != nil || !json.Valid(b) {
		t.Fatalf("literal dash file missing/invalid: %q, %v", b, err)
	}
}

func TestTraceOfflineExportPropagatesStdoutWriterError(t *testing.T) {
	root := t.TempDir()
	const id = "11111111111111111111111111111111"
	traceOfflineFixture(t, root, id, "launch", "abcd1234", 1795111105000000000, "")
	var errs bytes.Buffer
	rc := traceMain([]string{"trace", "export", id}, traceEnv{root: root}, traceExportFailWriter{}, &errs)
	if rc == 0 || !strings.Contains(errs.String(), "write") {
		t.Fatalf("writer failure was ignored: rc=%d stderr=%q", rc, errs.String())
	}
	if !strings.Contains(errs.String(), "Next:") ||
		!strings.Contains(errs.String(), "yolo trace show "+id) ||
		!strings.Contains(errs.String(), "yolo trace export "+id+" -o <file>") ||
		strings.Contains(errs.String(), "fixture output failure") {
		t.Fatalf("stdout failure has no safe actionable next step: %q", errs.String())
	}
}

func TestTraceOfflineExportParentRebindDuringEncoding(t *testing.T) {
	for _, failEncoding := range []bool{false, true} {
		name := "publish refusal cleans selected directory"
		if failEncoding {
			name = "encoder failure cleans selected directory"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			const id = "11111111111111111111111111111111"
			traceOfflineFixture(t, root, id, "launch", "abcd1234", 1795111105000000000, "")
			base := t.TempDir()
			parent := filepath.Join(base, "selected-parent")
			moved := filepath.Join(base, "moved-parent")
			if err := os.Mkdir(parent, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(parent, "trace.json")
			const old = "old complete trace\n"
			const impostor = "new-directory sentinel\n"
			if err := os.WriteFile(target, []byte(old), 0600); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseEncoder := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseEncoder)
			type result struct {
				rc  int
				out string
				err string
			}
			completed := make(chan result, 1)
			encoder := func(w io.Writer, _ *tracefile.Trace, _ string) error {
				close(started) // writeTraceExportFile has created the temporary in its selected parent.
				<-release
				if failEncoding {
					return fmt.Errorf("fixture encoder failure")
				}
				_, err := io.WriteString(w, "encoded trace\n")
				return err
			}
			go func() {
				var out, errs bytes.Buffer
				rc := traceMain([]string{"trace", "export", id, "-o", target}, traceEnv{root: root}, &out, &errs, encoder)
				completed <- result{rc: rc, out: out.String(), err: errs.String()}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Minute):
				t.Fatal("trace export did not reach the encoder callback")
			}
			if err := os.Rename(parent, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(parent, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(parent, "trace.json"), []byte(impostor), 0600); err != nil {
				t.Fatal(err)
			}
			releaseEncoder()
			var got result
			select {
			case got = <-completed:
			case <-time.After(5 * time.Minute):
				t.Fatal("trace export did not finish after encoder release")
			}
			if got.rc == 0 || got.out != "" || !strings.Contains(got.err, "Next:") ||
				strings.Contains(got.err, target) || strings.Contains(got.err, parent) || strings.Contains(got.err, moved) {
				t.Fatalf("rebound parent was not safely refused: rc=%d stdout=%q stderr=%q", got.rc, got.out, got.err)
			}
			if b, err := os.ReadFile(filepath.Join(moved, "trace.json")); err != nil || string(b) != old {
				t.Fatalf("original output changed: %q, %v", b, err)
			}
			if b, err := os.ReadFile(filepath.Join(parent, "trace.json")); err != nil || string(b) != impostor {
				t.Fatalf("rebound directory sentinel changed: %q, %v", b, err)
			}
			for dir, want := range map[string]string{moved: "trace.json", parent: "trace.json"} {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 || entries[0].Name() != want {
					t.Fatalf("temporary artifact orphaned in %s: entries=%v err=%v", dir, entries, err)
				}
			}
		})
	}
}

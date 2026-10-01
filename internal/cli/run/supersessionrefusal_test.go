package run

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// supersessionrefusal_test.go is docs/design/reference-mismatch-diagnostics.md §7 steps 4
// and 5: the launch REFUSES a selected pack's `supersedes` claim that matches no capability
// any loophole of its pack set serves, with the discovery warning's own sentence, the fix,
// and the skew clause when version.SourceSkew proves one.
//
// Through Run, not refuseUnmatchedSupersessions: the gate's callee has its own tests in
// internal/loopholes, and a test of it alone stays green with the pre-flight deleted.

// supersessionFixture configures two local packs: `acme`, shipping a loophole that serves
// acme-oauth-refresh, and `acme-bedrock`, superseding the capability `claimed`.
func supersessionFixture(t *testing.T, claimed string) {
	t.Helper()
	home := packHome(t)
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, "")
	}
	base := t.TempDir()
	acme := filepath.Join(base, "acme")
	module := filepath.Join(acme, "loopholes", "acme-broker")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, acme, `{"name":"acme","contributes":[{"kind":"loophole","from":"loopholes/acme-broker"}]}`)
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(
		`{"name":"acme-broker","description":"acme's refresher","transport":"none",`+
			`"default_enabled":true,"serves":["acme-oauth-refresh"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bedrock := filepath.Join(base, "acme-bedrock")
	if err := os.MkdirAll(bedrock, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, bedrock, `{"name":"acme-bedrock","supersedes":[{"capability":"`+claimed+`",`+
		`"because":"Bedrock overrides the OAuth path"}]}`)
	writeUserPacks(t, home, `[{"source":"file://`+acme+`","name":"acme"},`+
		`{"source":"file://`+bedrock+`","name":"acme-bedrock"}]`)
}

// macosUserReached installs a MacosUserRun that records whether the dispatch happened.
func macosUserReached(o *Options) *bool {
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		reached = true
		return 0
	}
	return &reached
}

// TestLaunchRefusesAnUnmatchedSupersession is the call-site test. A claim naming a
// capability no loophole serves retires nothing, and the author believes it worked; the
// launch now stops, BEFORE the backend dispatch, with the sentence that names the typo,
// suggests the served capability and lists what is served.
//
// It asserts the refusal carries the sentence once. That nothing prints it BEFORE the refusal
// is TestLaunchRefusalIsTheOnlyPrintingOfItsSentence's to pin: discovery warns on the process's
// stderr, which this test does not read, and this fixture has no via profile, so nothing on the
// launch path discovers before staging ends.
func TestLaunchRefusesAnUnmatchedSupersession(t *testing.T) {
	supersessionFixture(t, "acme-oauth-refersh")
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	reached := macosUserReached(o)

	rc := Run(*o)

	out := stdout.String() + stderr.String()
	if rc != 1 {
		t.Fatalf("Run() = %d, want 1: an unmatched supersession must refuse the launch\n%s", rc, out)
	}
	if *reached {
		t.Fatal("the backend was dispatched: the refusal must land before any backend starts")
	}
	for _, want := range []string{
		unmatchedSupersessionHeader,
		"pack 'acme-bedrock' supersedes capability 'acme-oauth-refersh'",
		"did you mean 'acme-oauth-refresh'",
		"Served here: [acme-oauth-refresh]",
		loopholes.UnmatchedSupersessionRemedy,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "supersedes capability 'acme-oauth-refersh'"); n != 1 {
		t.Errorf("the sentence appears %d times in the launch's output, want once:\n%s", n, out)
	}
	if strings.Contains(out, "older than the source tree it builds from") {
		t.Errorf("the refusal names skew no comparison proved:\n%s", out)
	}
}

// TestLaunchAcceptsAMatchedSupersession: the same packs with the capability spelled right
// launch. A gate that refused a correct claim would refuse every launch that supersedes.
func TestLaunchAcceptsAMatchedSupersession(t *testing.T) {
	supersessionFixture(t, "acme-oauth-refresh")
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	reached := macosUserReached(o)

	if rc := Run(*o); rc != 0 || !*reached {
		t.Fatalf("Run() = %d (dispatched=%v), want a launch: the claim matches a served "+
			"capability\nstdout:\n%s\nstderr:\n%s", rc, *reached, stdout.String(), stderr.String())
	}
	if out := stdout.String() + stderr.String(); strings.Contains(out, unmatchedSupersessionHeader) {
		t.Errorf("a matched claim was refused:\n%s", out)
	}
}

// TestLaunchRefusalNamesProvenSkew is §7 step 5 (RM-D2), on a container backend: with
// YOLO_ALLOW_SOURCE_SKEW=1 the launch gets past the source-skew gate with a yolo older than
// its source, and a claim that matches nothing may be the older yolo's packs lacking a
// capability rather than a typo. So the refusal names both commits and `just install` — and
// it reads version.SourceSkew itself, since the gate that would have said so was overruled.
func TestLaunchRefusalNamesProvenSkew(t *testing.T) {
	repoRoot := skewRepo(t)
	installed := version.GitCommit
	head := headCommit(t, repoRoot)
	supersessionFixture(t, "acme-oauth-refersh")

	var stdout, stderr bytes.Buffer
	o := skewOptions(t, repoRoot, map[string]string{AllowSourceSkewEnv: "1"}, &stdout, &stderr)

	rc := Run(*o)

	out := stdout.String() + stderr.String()
	if rc != 1 {
		t.Fatalf("Run() = %d, want 1\n%s", rc, out)
	}
	for _, want := range []string{
		unmatchedSupersessionHeader,
		"supersedes capability 'acme-oauth-refersh'",
		"This yolo is older than the source tree it builds from",
		"yolo: " + installed[:8],
		"tree: " + head[:8],
		"(cd " + shquote.Quote(repoRoot) + " && just install)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, out)
		}
	}
}

// TestLaunchRefusalIsTheOnlyPrintingOfItsSentence pins where the gate sits (RM-D3): ahead of
// checkViaRoutes, whose composition discovers loopholes through NewHostSet when a via profile
// is active, and whose discovery warns an unmatched claim on the PROCESS's stderr
// (loopholes.warnf) rather than on o.Stderr. So the fixture selects the shipped claude pack
// with bedrock-bridge, a via profile, and the count reads the process's stderr too: a gate
// moved below checkViaRoutes prints the sentence twice, once as a warning the refusal then
// contradicts. TestLaunchRefusesAnUnmatchedSupersession cannot see this: its fixture has no
// via, so nothing discovers before staging ends, and it reads only o.Stdout and o.Stderr.
//
// The pack name is this test's own, because warnf says each distinct line once per process:
// a sentence another test had already warned would be silent here, and the count would pass
// with the gate misplaced.
func TestLaunchRefusalIsTheOnlyPrintingOfItsSentence(t *testing.T) {
	home := packHome(t)
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, "")
	}
	typo := filepath.Join(t.TempDir(), "claude-bedrock-via-typo")
	if err := os.MkdirAll(typo, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, typo, `{"name":"claude-bedrock-via-typo","supersedes":[{"capability":`+
		`"claude-oauth-refersh","because":"Bedrock overrides the OAuth path"}]}`)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{"packs": ["claude", `+
		`{"source":"file://`+typo+`","name":"claude-bedrock-via-typo"}], `+
		`"profile": {"claude": "bedrock-bridge"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	reached := macosUserReached(o)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = saved })
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	rc := Run(*o)
	os.Stderr = saved
	_ = w.Close()
	process := <-done
	_ = r.Close()

	out := stdout.String() + stderr.String()
	if rc != 1 || *reached {
		t.Fatalf("Run() = %d (dispatched=%v), want the refusal\n%s\n%s", rc, *reached, out, process)
	}
	if !strings.Contains(out, unmatchedSupersessionHeader) {
		t.Fatalf("the launch refused, but not over the supersession:\n%s\n%s", out, process)
	}
	sentence := "pack 'claude-bedrock-via-typo' supersedes capability 'claude-oauth-refersh'"
	if n := strings.Count(out+process, sentence); n != 1 {
		t.Errorf("the sentence appears %d times across the launch's streams and the process's "+
			"stderr, want once (the refusal only):\nlaunch:\n%s\nprocess stderr:\n%s", n, out, process)
	}
}

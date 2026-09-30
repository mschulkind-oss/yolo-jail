package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
// It asserts the sentence appears EXACTLY ONCE across both streams: the gate runs ahead of
// every NewHostSet on the launch path, whose discovery warns the same sentence, so a launch
// that printed it as a warning and then refused over it would say one thing twice.
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
		t.Errorf("the sentence appears %d times, want once (the refusal only):\n%s", n, out)
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
		"(cd " + repoRoot + " && just install)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, out)
		}
	}
}

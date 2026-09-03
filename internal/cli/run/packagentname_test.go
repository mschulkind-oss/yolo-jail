package run

// packagentname_test.go is the SEVENTH launch pre-flight (briefing-audiences.md OQ-BA6/BA7):
// an agent NAME claimed by two packs.
//
// Everything here drives the real stagePacks rather than packload.AgentNameCollisions, which
// is the point: the pass has its own unit tests in internal/packload, and a test that only
// exercised the pass would stay green with the pre-flight deleted — the shape AGENTS.md
// records this repo shipping five times.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoLocalPacks configures two local packs with the given manifests, in order, and returns
// Options ready for stagePacks.
func twoLocalPacks(t *testing.T, aName, aManifest, bName, bManifest string) *Options {
	t.Helper()
	home := packHome(t)
	base := t.TempDir()
	var entries []string
	for _, p := range []struct{ name, manifest string }{{aName, aManifest}, {bName, bManifest}} {
		dir := filepath.Join(base, p.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePack(t, dir, p.manifest)
		entries = append(entries, `{"source":"file://`+dir+`","name":"`+p.name+`"}`)
	}
	writeUserPacks(t, home, "["+strings.Join(entries, ",")+"]")
	return &Options{Workspace: t.TempDir(), Stdout: &strings.Builder{}}
}

// THE CALL-SITE TEST. Two packs, one name, two different kinds — the launch must refuse, and
// the message must be usable without opening a design doc.
func TestLaunchRefusesTwoPacksClaimingOneAgentName(t *testing.T) {
	o := twoLocalPacks(t,
		"claude-official", `{"contributes":[
			{"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"}]}`,
		"claude-fork", `{"contributes":[
			{"kind":"briefing","into":".claude/CLAUDE.md","agent":"claude"}]}`)

	_, _, _, err := o.stagePacks("yolo-test-agentname")
	if err == nil {
		t.Fatal("stagePacks accepted two packs claiming the agent name `claude` — every " +
			"consumer of that name (`-p claude=…`, `use_profiles.claude`, an " +
			"`agents: [\"claude\"]` selector) resolves it by literal against whichever " +
			"declaration it reads first, so this launch is silently ambiguous")
	}
	msg := err.Error()
	for _, want := range []string{
		"agent name claude",
		"claude-fork",
		"claude-official",
		"exactly ONE owning pack",
		"cannot both be the claude pack",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q; got:\n%s", want, msg)
		}
	}
}

// The happy path, and the one that would break every stock config if the pass over-claimed:
// two packs owning two DIFFERENT names, each claiming its own through several kinds at once.
func TestLaunchAcceptsTwoPacksOwningDifferentAgentNames(t *testing.T) {
	o := twoLocalPacks(t,
		"alpha", `{"contributes":[
			{"kind":"program","bin":"alphacli","via":"npm","package":"alphacli"},
			{"kind":"launch","bin":"alphacli","flags":["--yes"]},
			{"kind":"briefing","into":".alpha/AGENTS.md","agent":"alphacli"}]}`,
		"beta", `{"contributes":[
			{"kind":"program","bin":"betacli","via":"npm","package":"betacli"},
			{"kind":"skills","into":".beta/skills","agent":"betacli"}]}`)

	if _, _, _, err := o.stagePacks("yolo-test-agentname-ok"); err != nil {
		t.Fatalf("two packs owning two names must launch; got: %v", err)
	}
}

// And the shape that must NOT be refused, because it is the most ordinary dependency a pack
// declares: a content pack asserting the binary the agent pack installs. `requires` is
// CombineShared, and counting it as an ownership claim would refuse this
// (packload.AgentNameCollisions' docstring has the reductio).
func TestLaunchAcceptsAContentPackRequiringTheAgentsBinary(t *testing.T) {
	o := twoLocalPacks(t,
		"claude", `{"contributes":[
			{"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"},
			{"kind":"briefing","into":".claude/CLAUDE.md","agent":"claude"}]}`,
		"house-rules", `{"contributes":[
			{"kind":"requires","bin":"claude"},
			{"kind":"briefing","from":"prose/claude.md","agents":["claude"]}]}`)

	if _, _, _, err := o.stagePacks("yolo-test-agentname-requires"); err != nil {
		t.Fatalf("a content pack that ASSERTS claude and addresses prose to it owns nothing "+
			"and must launch; got: %v", err)
	}
}

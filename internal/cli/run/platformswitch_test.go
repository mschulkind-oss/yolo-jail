package run

// platformswitch_test.go pins PP-D1 (docs/design/providers-and-profiles-redesign.md, ruled
// 2026-09-29) at the jail notch: when the user's own ~/.claude/settings.json turns on
// CLAUDE_CODE_USE_BEDROCK and claude's selected provider is not Bedrock, so the credential gate
// sends claude no Bedrock credential, every arm prints ONE line naming the conflict and both fixes,
// and changes nothing else. Each arm is driven through the code a launch runs — Run() on
// macos-user, Run() down the podman path, deliverChannelOnAttach — so deleting its call site fails
// here.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// switchLine is the part of the line every arm must print.
const switchLine = "claude: ~/.claude/settings.json sets CLAUDE_CODE_USE_BEDROCK, which puts claude on " +
	"its own \"aws-bedrock\" client, but no \"aws-bedrock\" provider is selected for claude, so yolo " +
	"delivers it none of that platform's credentials: select one (-p bedrock), or remove " +
	"CLAUDE_CODE_USE_BEDROCK from ~/.claude/settings.json (yolo leaves it alone)."

// writeHostClaudeSettings writes the user's own host ~/.claude/settings.json under home.
func writeHostClaudeSettings(t *testing.T, home, body string) {
	t.Helper()
	p := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// THE macos-user ARM: a launch with no Bedrock selection prints the line and runs; the same
// launch on `bedrock` (whose provider is Bedrock) prints nothing, nor does a switch that is off.
func TestTheMacosUserLaunchNamesAUsersOwnBedrockSwitch(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["claude"]}`, shellWith(nil))
	o.ProfileName = ""
	writeHostClaudeSettings(t, seen.home, `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`)
	if rc := Run(*o); rc != 0 || !seen.reached {
		t.Fatalf("the line is a disclosure, never a refusal: rc=%d reached=%v\n%s", rc, seen.reached, stderr)
	}
	if got := stderr.String(); strings.Count(got, switchLine) != 1 {
		t.Errorf("the macos-user launch must print the conflict once:\n%s", got)
	}
	if data, _ := os.ReadFile(filepath.Join(seen.home, ".claude", "settings.json")); !strings.Contains(string(data), "CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("yolo deletes nothing it did not write, and the user's key went: %s", data)
	}

	o, stderr, seen = overrideNativeLaunch(t, `{"packs": ["claude"], "providers": {"bedrock": {"region": "us-west-2"}}}`, shellWith(nil))
	writeHostClaudeSettings(t, seen.home, `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`)
	if rc := Run(*o); rc != 0 || strings.Contains(stderr.String(), "CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("claude on bedrock serves its own switch, so nothing is said: rc=%d\n%s", rc, stderr)
	}
}

// THE ATTACH ARM prints it too, on an entry whose selection does not serve the switch.
func TestAnAttachNamesAUsersOwnBedrockSwitch(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "bedrock")}
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, emptyEnv(), nil)
	writeHostClaudeSettings(t, os.Getenv("HOME"), `{"env": {"CLAUDE_CODE_USE_BEDROCK": "true"}}`)
	if rc := o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel); rc != 0 {
		t.Fatalf("the attach must deliver: rc=%d\n%s", rc, stderr)
	}
	if !strings.Contains(stderr.String(), switchLine) {
		t.Errorf("the attach must name the conflict:\n%s", stderr)
	}

	// A SWITCH yolo WROTE (HC-D25): with the host's computed-leaf record naming the value the file
	// holds, the line is the one naming `yolo host apply`'s write, never the user-owned one. The
	// record is planted at the owned contract's path, which is every contract's (the reader,
	// render.HostLeafWrote, resolves it unstated), so a record the retired `assert` left reads
	// the same.
	o, cfg, channel, stderr = attachFixture(t, currentJailEnv, packs, emptyEnv(), nil)
	writeHostClaudeSettings(t, os.Getenv("HOME"), `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`)
	rec := render.Host(os.Getenv("HOME"), nil, render.OwnershipOwn).LeafRecordPath("claude", "settings")
	if err := os.MkdirAll(filepath.Dir(rec), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec, []byte(`{"/env/CLAUDE_CODE_USE_BEDROCK": "1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg, stagedPacks{root: "/ctx/packs", packs: packs}, channel)
	if got := stderr.String(); !strings.Contains(got, "which `yolo host apply` wrote there for claude's host selection") ||
		strings.Contains(got, "yolo leaves it alone") {
		t.Errorf("a switch yolo's host apply wrote must be named as yolo's:\n%s", got)
	}
	if err := os.Remove(rec); err != nil {
		t.Fatal(err)
	}

	// A switch that is off says nothing.
	o, cfg, channel, stderr = attachFixture(t, currentJailEnv, packs, emptyEnv(), nil)
	writeHostClaudeSettings(t, os.Getenv("HOME"), `{"env": {"CLAUDE_CODE_USE_BEDROCK": "0"}}`)
	o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg, stagedPacks{root: "/ctx/packs", packs: packs}, channel)
	if strings.Contains(stderr.String(), "CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("a switch that is off is no conflict:\n%s", stderr)
	}
}

// THE FRESH CONTAINER ARM, through Run() down the podman path with no podman on PATH: claude on
// zai with no key, so the credential pre-flight refuses right after the note — which must already
// have been printed, since a refused launch is where a user most needs to know.
func TestTheFreshPodmanLaunchNamesAUsersOwnBedrockSwitch(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["claude", "zai"]}`)
	writeHostClaudeSettings(t, home, `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`)
	t.Setenv("PATH", t.TempDir())
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.Args = []string{"claude"}
	o.ProfileName = "zai"
	o.Getenv = shellWith(nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) >= 2 && argv[1] == "info" {
			return ExecResult{Ran: true, RC: 0, Stdout: "host: {}"}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
		return image.LoadResult{OK: true, Ref: goldenImageRef}
	}
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want the credential refusal's 1\nstderr:\n%s", rc, stderr.String())
	}
	got := stderr.String()
	if !strings.Contains(got, "ZAI_API_KEY") {
		t.Fatalf("fixture: the launch must reach the credential pre-flight:\n%s", got)
	}
	note := strings.Index(got, switchLine)
	if note < 0 || note > strings.Index(got, "ZAI_API_KEY") {
		t.Errorf("the fresh podman launch must name the conflict before the pre-flight's refusal:\n%s", got)
	}
}

package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// `yolo init` writes a comment-only pointer file now (TestInitWritesPointerAndInfoExcludeOnly),
// so the claims about blocked tools and where the agent runs it once carried survive only in the
// user-scope scaffold below, which keeps its own copy of the setup comment.

// initUserConfigFile runs `yolo init-user-config` against a throwaway HOME and returns the
// file it wrote — the user-scope sibling of the workspace template, carrying its own copy of
// the setup comment. It goes through the command registry rather than calling
// InitUserConfig, so unwiring the subcommand fails this as surely as a stale word does.
func initUserConfigFile(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cmd, ok := registry["init-user-config"]
	if !ok {
		t.Fatal("`yolo init-user-config` is not in the command registry")
	}
	if code := cmd(nil); code != 0 {
		t.Fatalf("`yolo init-user-config` exited %d", code)
	}
	b, err := os.ReadFile(paths.UserConfigDisplayPath())
	if err != nil {
		t.Fatalf("`yolo init-user-config` wrote no user config: %v", err)
	}
	return string(b)
}

// commentBlockBefore returns the run of `#` comment lines immediately above the first line
// containing marker — the prose a reader sees next to that key — as one lowercased line, with
// the comment markers stripped and the wrapping undone, so a phrase can be matched wherever
// the text happens to break.
func commentBlockBefore(t *testing.T, body, marker string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	at := -1
	for i, l := range lines {
		if strings.Contains(l, marker) {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatalf("the scaffolded config has no %q — this test has lost its subject", marker)
	}
	start := at
	for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "#") {
		start--
	}
	var words []string
	for _, l := range lines[start:at] {
		words = append(words, strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), "#"))...)
	}
	return strings.ToLower(strings.Join(words, " "))
}

// TestInitTemplateOffersSetupsItCanRun ties the user-scope scaffold's "where the agent runs"
// comment to the setups yolo resolves: `yolo init-user-config` offers a `setup` line, every name
// its comment quotes is one setup.Lookup accepts, and it offers no `runtime` line, a key a config
// carrying is refused (named-setups-plan.md slice 5). It once offered "podman" or "container" long
// after macos-user shipped, and before that docker, which validation refused.
func TestInitTemplateOffersSetupsItCanRun(t *testing.T) {
	body := initUserConfigFile(t)
	if strings.Contains(body, "runtime =") {
		t.Errorf("the config `yolo init-user-config` writes offers the removed runtime key:\n%s", body)
	}
	block := commentBlockBefore(t, body, `# setup =`)
	quoted := regexp.MustCompile(`"([a-z0-9-]+)"`).FindAllStringSubmatch(block, -1)
	if len(quoted) == 0 {
		t.Fatalf("the setup comment quotes no setup name:\n%s", block)
	}
	for _, m := range quoted {
		if _, err := setup.Lookup(m[1], nil); err != nil {
			t.Errorf("the setup comment offers %q, which is no setup: %v\n%s", m[1], err, block)
		}
	}
}

// TestEveryKeyTheUserTemplateOffersIsAcceptedTOML: each commented-out key line of the
// scaffold, uncommented, is TOML that `yolo config check` accepts, so the template teaches no
// spelling the reader refuses (LC-D12: it writes the TOML format, and its examples are that
// format's).
func TestEveryKeyTheUserTemplateOffersIsAcceptedTOML(t *testing.T) {
	body := initUserConfigFile(t)
	key := regexp.MustCompile(`^# ?((\[[a-z_.]+\])|([a-z_]+ = .*))$`)
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if m := key.FindStringSubmatch(l); m != nil {
			lines = append(lines, m[1])
		}
	}
	if len(lines) < 5 {
		t.Fatalf("found only %d offered key lines in the scaffold; the test has lost its input:\n%s", len(lines), body)
	}
	if err := os.WriteFile(paths.UserConfigDisplayPath(), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if rc, out, errOut := captureDispatchRC(t, []string{"config", "check"}); rc != 0 {
		t.Fatalf("`yolo config check` refuses the scaffold's offered keys (rc %d):\n%s\n%s%s",
			rc, strings.Join(lines, "\n"), out, errOut)
	}
}

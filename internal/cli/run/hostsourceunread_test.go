package run

// THE JAIL-LAUNCH LINE for a host file a selected pack declared the jail reads and the launch
// did not (docs/design/agent-directory-map.md §4.2, §6.4 item 5, §6.5 item 2). The incident it
// answers: a dotfiles manager left ~/.pi/agent/settings.json as a link into a deleted
// ~/.dotfiles/pi, and every jail launch composed pi's settings without the user's host layer
// while saying nothing, because the source probe (isFile, an os.Stat) cannot tell a dangling
// link from a file the user never created.
//
// Every test here drives a production caller — assembleRunCmd, buildMacosCtxTree,
// refreshJailBriefings — so deleting the line at any of the three call sites fails one.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// danglingIncidentHome is the incident's home: ~/.pi/agent/<name> is a link into
// ~/.dotfiles/pi, which does not exist. It returns the home and the link's target.
func danglingIncidentHome(t *testing.T, name string) (home, target string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	agent := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(home, ".dotfiles", "pi", name)
	if err := os.Symlink(target, filepath.Join(agent, name)); err != nil {
		t.Fatal(err)
	}
	return home, target
}

// assembleForPi runs the container assembler with the shipped pi pack selected and returns the
// argv and everything the assembler printed to stderr.
func assembleForPi(t *testing.T, home string) ([]string, string) {
	t.Helper()
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	var buf bytes.Buffer
	o.Stderr = &buf
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("security", sec),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		packs:        packsFixture(t, "pi"),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	return argv, buf.String()
}

// linesMentioning returns the lines of out that contain needle.
func linesMentioning(out, needle string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, needle) {
			got = append(got, l)
		}
	}
	return got
}

// §6.5 item 2, on the container backends: ONE line, naming the link and its target, and the
// jail still starts — the argv is built, and the source is not mounted.
func TestAJailLaunchNamesADanglingHostSettingsLink(t *testing.T) {
	home, target := danglingIncidentHome(t, "settings.json")

	argv, printed := assembleForPi(t, home)

	got := linesMentioning(printed, "~/.pi/agent/settings.json")
	if len(got) != 1 {
		t.Fatalf("want exactly one launch line naming ~/.pi/agent/settings.json, got %d:\n%s",
			len(got), printed)
	}
	t.Log(got[0])
	for _, want := range []string{"pack pi", target, "does not exist", "without it"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the launch line does not say %q:\n%s", want, got[0])
		}
	}
	if len(argv) == 0 {
		t.Fatal("the launch built no argv: a dangling host file must never stop the jail")
	}
	for _, a := range argv {
		if strings.Contains(a, "/ctx/host-pi/settings.json") {
			t.Errorf("a dangling source was mounted: %q", a)
		}
	}
}

// A dotfiles manager can link the whole directory instead of the file. The source path then
// does not exist at all, which isFile reads as "not created yet"; the link that explains it is
// an ancestor, and the line names that one.
func TestAJailLaunchNamesADanglingAncestorLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".dotfiles", "pi")
	if err := os.Symlink(target, filepath.Join(home, ".pi")); err != nil {
		t.Fatal(err)
	}

	_, printed := assembleForPi(t, home)

	got := linesMentioning(printed, "~/.pi/agent/settings.json")
	if len(got) != 1 {
		t.Fatalf("want exactly one launch line naming ~/.pi/agent/settings.json, got %d:\n%s",
			len(got), printed)
	}
	if want := filepath.Join(home, ".pi") + " is a symlink to " + target; !strings.Contains(got[0], want) {
		t.Errorf("the launch line does not name the ancestor link (%q):\n%s", want, got[0])
	}
}

// The NORMAL state stays silent: a host file the user has not created is not a finding, and a
// line for it would print on nearly every launch.
func TestAJailLaunchSaysNothingAboutAnAbsentHostFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, printed := assembleForPi(t, home)

	if got := linesMentioning(printed, "settings.json"); len(got) != 0 {
		t.Errorf("an absent host file produced a launch line:\n%s", strings.Join(got, "\n"))
	}
}

// macos-user stages the same grants by copy (buildMacosCtxTree) and skipped a dangling source
// with the same isFile probe. Same line, and still no failure.
func TestAMacosUserLaunchNamesADanglingHostSettingsLink(t *testing.T) {
	_, target := danglingIncidentHome(t, "settings.json")
	var buf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }, Stderr: &buf}

	delivery, err := o.buildMacosCtxTree(t.TempDir(), packsFixture(t, "pi"), jsonx.NewOrderedMap())
	if err != nil {
		t.Fatalf("buildMacosCtxTree: %v — a dangling host file must never stop the launch", err)
	}
	for _, d := range delivery.ctx.Delivered {
		if strings.HasSuffix(d, "/settings.json") {
			t.Errorf("a dangling source was recorded as delivered: %q", d)
		}
	}
	got := linesMentioning(buf.String(), "~/.pi/agent/settings.json")
	if len(got) != 1 {
		t.Fatalf("want exactly one launch line naming ~/.pi/agent/settings.json, got %d:\n%s",
			len(got), buf.String())
	}
	if !strings.Contains(got[0], target) {
		t.Errorf("the launch line does not name the link's target %q:\n%s", target, got[0])
	}
}

// stagePiBriefing composes the pi pack's briefing for workspace ws out of a jail and returns the
// staged text and what the launch printed to stderr.
func stagePiBriefing(t *testing.T, home, ws string) (string, string) {
	t.Helper()
	pinLauncherInJail(t, false)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	var buf bytes.Buffer
	o.Stderr = &buf
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", newConfig(), "podman",
		stagedPacks{packs: packsFixture(t, "pi")}, ioprio.Normal)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v — a dangling host briefing must never stop the launch", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, briefingStagingName(".pi/agent/AGENTS.md")))
	if err != nil {
		t.Fatalf("no briefing was staged for pi: %v", err)
	}
	return string(body), buf.String()
}

// The briefing half: pi's `after: "host:.pi/agent/AGENTS.md"` behind a dangling link is named,
// and the briefing is still written, without the host file.
func TestAJailLaunchNamesADanglingHostBriefingLink(t *testing.T) {
	home, target := danglingIncidentHome(t, "AGENTS.md")
	ws := t.TempDir()

	body, printed := stagePiBriefing(t, home, ws)

	got := linesMentioning(printed, "~/.pi/agent/AGENTS.md")
	if len(got) != 1 {
		t.Fatalf("want exactly one launch line naming ~/.pi/agent/AGENTS.md, got %d:\n%s",
			len(got), printed)
	}
	t.Log(got[0])
	for _, want := range []string{"briefing", target, "does not exist", "without it"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the launch line does not say %q:\n%s", want, got[0])
		}
	}
	// The briefing is the one a home with no host file gets, byte for byte.
	if err := os.Remove(filepath.Join(home, ".pi", "agent", "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if without, _ := stagePiBriefing(t, home, ws); body != without {
		t.Errorf("the briefing staged behind a dangling link differs from the one staged with no "+
			"host file:\n--- dangling ---\n%s\n--- absent ---\n%s", body, without)
	}
}

// A host briefing that is there but cannot be read as a file used to be dropped as silently as
// an absent one (PrependHostBriefing returned the jail content on ANY read error). A directory
// is the portable way to make that read fail, root or not.
func TestAJailLaunchNamesAHostBriefingThatIsNotAFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent", "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, printed := stagePiBriefing(t, home, t.TempDir())

	got := linesMentioning(printed, "~/.pi/agent/AGENTS.md")
	if len(got) != 1 {
		t.Fatalf("want exactly one launch line naming ~/.pi/agent/AGENTS.md, got %d:\n%s",
			len(got), printed)
	}
	if !strings.Contains(got[0], "not a regular file") {
		t.Errorf("the launch line does not say what is wrong:\n%s", got[0])
	}
}

// And the user's own readable host briefing is still prepended, with no line: the check must
// not turn the feature off.
func TestAReadableHostBriefingIsStillPrependedSilently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agent := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	// Through a link that resolves, which is how a dotfiles manager delivers it when nothing
	// is broken.
	real := filepath.Join(home, ".dotfiles", "pi", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("MY OWN PI RULES\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(agent, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	body, printed := stagePiBriefing(t, home, t.TempDir())

	if !strings.HasPrefix(body, "MY OWN PI RULES\n") {
		t.Errorf("the user's own host briefing was not prepended:\n%s", body)
	}
	if got := linesMentioning(printed, "AGENTS.md"); len(got) != 0 {
		t.Errorf("a readable host briefing produced a launch line:\n%s", strings.Join(got, "\n"))
	}
}

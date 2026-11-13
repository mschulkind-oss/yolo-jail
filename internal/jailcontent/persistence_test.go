package jailcontent

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// THE HOME LINE SAID "(persistent across sessions)" ON EVERY BACKEND, and on podman the home
// is a read-only bind: the claim invited writes that fail, and the agent fell back to /tmp,
// which a restart deletes (docs/reference/jail-home.md#storage-classes-and-the-durable-dir). No backend, notch or
// hand-built input may say it again.
func TestNoBriefingCallsTheHomePersistent(t *testing.T) {
	for _, in := range []BriefingInput{
		{Workspace: "/w"},
		{Workspace: "/w", Mechanism: "podman"},
		{Workspace: "/w", Mechanism: "podman", Persistence: podmanShapedMap()},
		{Workspace: "/w", Mechanism: "container"},
		{Workspace: "/Users/Shared/yolo/p", Mechanism: "macos-user", Home: "/Users/_yolojail"},
		{Setup: setup.MustBuiltIn("host-self")},
	} {
		if out := reachable(in); strings.Contains(out, "persistent across sessions") {
			t.Errorf("mechanism %q, setup %q: the briefing still calls the home persistent:\n%s",
				in.Mechanism, in.Setup.Name, environmentBlockOf(t, out))
		}
	}
}

// macos-user's Home line says the fact that matters there: the home is THIS workspace's own
// `.yolo/home` in both modes since slice 12a (macos-user-model-plan.md MMP-D25), and only its links
// to machine-wide state are shared. It said "one account home, shared by every workspace" until
// the Mac run of 2026-11-03 found that text in a 12a briefing. Its section is a later slice.
func TestTheMacosUserHomeLineSaysItIsTheWorkspacesOwn(t *testing.T) {
	out := reachable(BriefingInput{Workspace: "/Users/Shared/yolo/p", Mechanism: "macos-user",
		Home: "/Users/Shared/yolo/p/.yolo/home"})
	env := environmentBlockOf(t, out)
	if !strings.Contains(env, "`/Users/Shared/yolo/p/.yolo/home` (this workspace's own") ||
		!strings.Contains(env, "shared by every workspace on this machine") {
		t.Errorf("the macos-user Home line does not say the home is this workspace's own and "+
			"its machine-wide links shared:\n%s", env)
	}
	if strings.Contains(out, "one account home") || strings.Contains(out, "are links into this workspace") {
		t.Errorf("the macos-user Home line still describes the pre-12a shared account home:\n%s", env)
	}
	if strings.Contains(out, persistenceHeading) {
		t.Errorf("macos-user got a container-shaped section it has no map for:\n%s", out)
	}
}

// A class with no members renders no bullet, and a nil map renders no section at all.
func TestThePersistenceSectionDropsEmptyClasses(t *testing.T) {
	m := &PersistenceMap{Paths: []PersistentPath{{"/workspace", PathProject}, {"/tmp", PathPerLaunch}}}
	sec := persistenceSectionOf(t, reachable(BriefingInput{Workspace: "/w", Persistence: m}))
	for _, gone := range []string{"**Every workspace on this machine**", "**Per workspace**"} {
		if strings.Contains(sec, gone) {
			t.Errorf("an empty class rendered a bullet %q:\n%s", gone, sec)
		}
	}
	if out := reachable(BriefingInput{Workspace: "/w"}); strings.Contains(out, "Storage classes") {
		t.Errorf("a nil map rendered the section, or the Home line pointed at one:\n%s", out)
	}
}

// Apple Container's shape: the whole home is per workspace, so there is no read-only bullet,
// and the per-launch set is in RAM.
func TestThePersistenceSectionForAWritableHome(t *testing.T) {
	m := &PersistenceMap{PerLaunchInRAM: true, Paths: []PersistentPath{
		{"/workspace", PathProject}, {"/home/agent", PathWorkspaceDurable},
		{"/home/agent/.cache", PathMachineDurable}, {"/tmp", PathPerLaunch},
	}}
	out := reachable(BriefingInput{Workspace: "/w", Mechanism: "container", Persistence: m})
	sec := persistenceSectionOf(t, out)
	for _, want := range []string{"all of `/home/agent` outside the other classes", "- **Per launch** (in RAM)"} {
		if !strings.Contains(sec, want) {
			t.Errorf("the section does not say %q:\n%s", want, sec)
		}
	}
	if strings.Contains(sec, "**Read-only**") {
		t.Errorf("a writable home got a read-only bullet:\n%s", sec)
	}
	if !strings.Contains(out, "- **Home**: `/home/agent` (writable, and kept for this workspace;") {
		t.Errorf("the Home line does not say the home is writable:\n%s", environmentBlockOf(t, out))
	}
}

func podmanShapedMap() *PersistenceMap {
	return &PersistenceMap{Paths: []PersistentPath{
		{"/workspace", PathProject},
		{"/workspace/.venv", PathWorkspaceDurable},
		{"/home/agent/go", PathWorkspaceDurable},
		{"/home/agent/.yolo/bin", PathInternal},
		{"/home/agent/.config", PathWorkspaceDurable},
		{"/home/agent/.claude", PathWorkspaceDurable},
		{"/home/agent/.cache", PathMachineDurable},
		{"/mise", PathMachineDurable},
		{"/tmp", PathPerLaunch},
		{"/run", PathPerLaunch},
	}}
}

func persistenceSectionOf(t *testing.T, briefing string) string {
	t.Helper()
	i := strings.Index(briefing, persistenceHeading)
	if i < 0 {
		t.Fatalf("no storage-classes section:\n%s", briefing)
	}
	rest := briefing[i:]
	if j := strings.Index(rest[1:], "\n## "); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

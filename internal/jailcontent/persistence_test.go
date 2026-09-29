package jailcontent

import (
	"strings"
	"testing"
)

// THE HOME LINE SAID "(persistent across sessions)" ON EVERY BACKEND, and on podman the home
// is a read-only bind: the claim invited writes that fail, and the agent fell back to /tmp,
// which a restart deletes (docs/design/durable-scratch-space.md §2.5). No backend, notch or
// hand-built input may say it again.
func TestNoBriefingCallsTheHomePersistent(t *testing.T) {
	podman := podmanShapedMap()
	for _, in := range []BriefingInput{
		{Workspace: "/w"},
		{Workspace: "/w", Mechanism: "podman"},
		{Workspace: "/w", Mechanism: "podman", Persistence: podman},
		{Workspace: "/w", Mechanism: "container"},
		{Workspace: "/Users/Shared/yolo/p", Mechanism: "macos-user", Home: "/Users/_yolojail"},
		{Confinement: "host"},
	} {
		if out := BriefingContent(in); strings.Contains(out, "persistent across sessions") {
			t.Errorf("mechanism %q, notch %q: the briefing still calls the home persistent:\n%s",
				in.Mechanism, in.Confinement, environmentBlockOf(t, out))
		}
	}
}

// macos-user's Home line says the fact that matters there: ONE account home serves every
// workspace (docs/design/durable-scratch-space.md §2.2).
func TestTheMacosUserHomeLineSaysItIsShared(t *testing.T) {
	out := BriefingContent(BriefingInput{Workspace: "/Users/Shared/yolo/p", Mechanism: "macos-user",
		Home: "/Users/_yolojail"})
	if !strings.Contains(out, "one account home, shared by every workspace on this") {
		t.Errorf("the macos-user Home line does not say the home is shared:\n%s", environmentBlockOf(t, out))
	}
	if strings.Contains(out, "## Durable vs ephemeral paths") {
		t.Errorf("macos-user got a container-shaped section it has no map for:\n%s", out)
	}
}

// The section renders each class from the map, in the design's order, and says what the
// Home line points at.
func TestThePersistenceSectionRendersTheMap(t *testing.T) {
	out := BriefingContent(BriefingInput{Workspace: "/w", Mechanism: "podman", Persistence: podmanShapedMap()})
	if !strings.Contains(out, "- **Home**: `/home/agent` (mostly read-only; see **Durable vs ephemeral paths** below)") {
		t.Errorf("the Home line does not point at the section:\n%s", environmentBlockOf(t, out))
	}
	sec := persistenceSectionOf(t, out)
	order := []string{
		"**Survives a restart, this workspace only**: `/workspace` (the host's own files); in home, only `~/.claude`, `~/.config`, `~/go`.",
		"**Survives, shared by every workspace on this machine**: `~/.cache`, `/mise`.",
		"**Read-only**: the rest of `/home/agent`",
		"**Deleted once this jail exits** (per launch; every attached terminal shares them): `/tmp`, `/run`.",
		"**Worktrees and anything you need next session**: inside `/workspace`",
	}
	last := -1
	for _, want := range order {
		i := strings.Index(sec, want)
		if i < 0 {
			t.Errorf("the section does not say %q:\n%s", want, sec)
			continue
		}
		if i < last {
			t.Errorf("%q is out of order:\n%s", want, sec)
		}
		last = i
	}
	// An internal entry is in the map and never offered as a place for work.
	if strings.Contains(sec, "~/.yolo/bin") {
		t.Errorf("the section names an internal path:\n%s", sec)
	}
	// Before every capability section, after Environment.
	if e, s, l := strings.Index(out, "## Environment"), strings.Index(out, "## Durable vs ephemeral paths"),
		strings.Index(out, "## Limitations"); !(e < s && s < l) {
		t.Errorf("the section is not between Environment and Limitations (%d, %d, %d)", e, s, l)
	}
}

// A class with no members renders no bullet, and a nil map renders no section at all.
func TestThePersistenceSectionDropsEmptyClasses(t *testing.T) {
	m := &PersistenceMap{Paths: []PersistentPath{{"/workspace", PathWorkspaceDurable}, {"/tmp", PathPerLaunch}}}
	sec := persistenceSectionOf(t, BriefingContent(BriefingInput{Workspace: "/w", Persistence: m}))
	if strings.Contains(sec, "shared by every workspace") {
		t.Errorf("an empty machine class rendered a bullet:\n%s", sec)
	}
	if strings.Contains(sec, "in home, only") {
		t.Errorf("an empty home list rendered a clause:\n%s", sec)
	}
	if out := BriefingContent(BriefingInput{Workspace: "/w"}); strings.Contains(out, "Durable vs ephemeral") {
		t.Errorf("a nil map rendered the section, or the Home line pointed at one:\n%s", out)
	}
}

// Apple Container's shape: the whole home is durable for the workspace, so there is no
// read-only bullet, and the per-launch set is in RAM.
func TestThePersistenceSectionForAWritableHome(t *testing.T) {
	m := &PersistenceMap{PerLaunchInRAM: true, Paths: []PersistentPath{
		{"/workspace", PathWorkspaceDurable}, {"/home/agent", PathWorkspaceDurable},
		{"/home/agent/.cache", PathMachineDurable}, {"/tmp", PathPerLaunch},
	}}
	out := BriefingContent(BriefingInput{Workspace: "/w", Mechanism: "container", Persistence: m})
	sec := persistenceSectionOf(t, out)
	for _, want := range []string{"all of `/home/agent` except the paths below", "(per launch, in RAM;"} {
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
		{"/workspace", PathWorkspaceDurable},
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
	i := strings.Index(briefing, "## Durable vs ephemeral paths")
	if i < 0 {
		t.Fatalf("no Durable vs ephemeral paths section:\n%s", briefing)
	}
	rest := briefing[i:]
	if j := strings.Index(rest[1:], "\n## "); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

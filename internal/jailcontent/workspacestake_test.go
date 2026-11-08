package jailcontent

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
)

// THE AGENT'S OWN STAKE (docs/design/workspace-state-safety.md SD-D2, SD-D3). An agent moved the
// workspace's `.yolo` to /tmp to rule it out while bisecting, and lost every session of the
// workspace at the restart: the section said the home dirs survive, never that they are stored
// in `.yolo`, that its own session is among them, or that a file MOVED to /tmp is lost too. Each
// container shape says all three, and names the supported diagnosis instead.
func TestTheSectionSaysTheAgentsSessionIsInDotYolo(t *testing.T) {
	const fresh = "To rule out this workspace's state in a diagnosis, do not move it: ask the human to run " +
		"`yolo --fresh-home`, or test from a throwaway workspace."
	const moved = "Throwaway files only, never a worktree, and never a holding place: a file moved here is gone at the restart."
	for _, c := range []struct {
		name, mechanism string
		m               *PersistenceMap
		stake           string
	}{
		{"podman", "podman", podmanShapedMap(), "Those home dirs, your own session among them, are stored in " +
			"`/workspace/.yolo/setups/<setup>/home`: never move, rename or delete `/workspace/.yolo` or anything in it " +
			"but your own files in `$YOLO_DURABLE_DIR`."},
		{"Apple Container", "container", appleContainerShapedMap(), "That home holds your own session too: never " +
			"move, rename or delete `/workspace/.yolo` or anything in it but your own files in `$YOLO_DURABLE_DIR`."},
	} {
		sec := persistenceSectionOf(t, BriefingContent(BriefingInput{Workspace: "/w", Mechanism: c.mechanism,
			Persistence: c.m, Durable: &DurableDir{Path: durable.ContainerJailPath}}))
		bullets := map[string]string{}
		for _, l := range strings.Split(sec, "\n") {
			for _, p := range []string{"- **Per launch**", "- **Per workspace**"} {
				if strings.HasPrefix(l, p) {
					bullets[p] = l
				}
			}
		}
		if !strings.HasSuffix(bullets["- **Per workspace**"], c.stake) {
			t.Errorf("%s: the per-workspace bullet does not end with the agent's stake %q:\n%s", c.name, c.stake, sec)
		}
		if !strings.HasSuffix(bullets["- **Per launch**"], moved) {
			t.Errorf("%s: the per-launch bullet does not say a moved file is lost:\n%s", c.name, sec)
		}
		if !strings.Contains(sec, "The rest of `/workspace` is the user's project.\n"+fresh+"\n") {
			t.Errorf("%s: the durable lead does not name the supported diagnosis:\n%s", c.name, sec)
		}
	}
}

// Without a durable dir there is no exception to offer: the stake forbids moving all of `.yolo`.
func TestTheStakeOffersNoDurableExceptionWithoutADurableDir(t *testing.T) {
	sec := persistenceSectionOf(t, BriefingContent(BriefingInput{Workspace: "/w", Mechanism: "podman",
		Persistence: podmanShapedMap(), Durable: &DurableDir{Unavailable: "x"}}))
	if !strings.Contains(sec, "never move, rename or delete `/workspace/.yolo` or anything in it.") {
		t.Errorf("the stake without a durable dir is not a plain prohibition:\n%s", sec)
	}
}

// The stake names the home the launch BOUND, read off the map's host sources: a launch that
// still binds the legacy `.yolo/home` says so, and one bound from a
// setup's home says the setup layout.
func TestTheStakeNamesTheHomeTheLaunchBound(t *testing.T) {
	const hostWS = "/srv/p"
	podman := func(home string) *PersistenceMap {
		m := podmanShapedMap()
		m.HostSources = map[string]string{
			"/home/agent/go":      home + "/go",
			"/home/agent/.config": home + "/config",
			"/home/agent/.claude": home + "/claude",
		}
		return m
	}
	for _, c := range []struct {
		name, mechanism string
		m               *PersistenceMap
		want, notWant   string
	}{
		{"podman legacy", "podman", podman(hostWS + "/.yolo/home"), "`/workspace/.yolo/home`:", "setups/<setup>"},
		{"podman setup", "podman", podman(hostWS + "/.yolo/setups/work/home"), "`/workspace/.yolo/setups/<setup>/home`:", ".yolo/home`"},
		// A `yolo --fresh-home` launch (SD-D14) is told its own fresh home, not the setup's usual one.
		{"podman fresh home", "podman", podman(hostWS + "/.yolo/setups/work/home.fresh-20261108T101500Z"),
			"`/workspace/.yolo/setups/work/home.fresh-20261108T101500Z`:", "setups/<setup>/home`"},
	} {
		sec := persistenceSectionOf(t, BriefingContent(BriefingInput{Workspace: hostWS, Mechanism: c.mechanism,
			Persistence: c.m, Durable: &DurableDir{Path: durable.ContainerJailPath}}))
		if !strings.Contains(sec, c.want) || strings.Contains(sec, c.notWant) {
			t.Errorf("%s: the section does not name the bound home as %q (or still says %q):\n%s", c.name, c.want, c.notWant, sec)
		}
	}
}

// macos-user mounts nothing, so a `mv` of `.yolo` moves the home itself: the header's home
// sentence carries the stake, and the Mac's own /tmp line says the workspace's state does not
// belong there either.
func TestTheMacosUserBriefingSaysTheAgentsSessionIsInDotYolo(t *testing.T) {
	ws := "/Users/Shared/yolo/p"
	out := BriefingContent(BriefingInput{Workspace: ws, Mechanism: "macos-user",
		Home: "/Users/_yolojail", Durable: &DurableDir{Path: ws + "/.yolo/durable"}})
	const stake = "Your own session is in that home: never move, rename or delete `.yolo` or anything in it " +
		"but your own files in `.yolo/durable`."
	if !strings.Contains(strings.Join(strings.Fields(out), " "), stake) {
		t.Errorf("the macos-user briefing lacks the agent's stake %q:\n%s", stake, out)
	}
	sec := persistenceSectionOf(t, out)
	if !strings.Contains(sec, "keep nothing there you need later, this workspace's `.yolo` least of all.") {
		t.Errorf("the macos-user /tmp line does not cover the workspace's state:\n%s", sec)
	}
	if !strings.Contains(sec, "`yolo --fresh-home`") {
		t.Errorf("the macos-user section does not name the supported diagnosis:\n%s", sec)
	}
}

// SD-D8: the diagnosing skill carries the recipe for ruling out the workspace's state, in every
// container and native rendering, and it never offers a move or a copy as the way.
func TestTheDiagnosingSkillRulesOutTheStateWithoutMovingIt(t *testing.T) {
	for _, mech := range []string{"podman", "container", "macos-user"} {
		got, err := RenderBuiltinSkills(FactSheet{WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.toml",
			Briefing: BriefingInput{Mechanism: mech}})
		if err != nil {
			t.Fatal(err)
		}
		skill := string(got["diagnosing-the-jail/SKILL.md"])
		i := strings.Index(skill, "## 8. Ruling out the workspace's state")
		if i < 0 {
			t.Errorf("%s: the diagnosing skill has no recipe for ruling out the workspace's state:\n%s", mech, skill)
			continue
		}
		recipe := skill[i:]
		for _, want := range []string{"never\nmove, rename, delete or copy `.yolo`", "`yolo --fresh-home`", "throwaway workspace"} {
			if !strings.Contains(recipe, want) {
				t.Errorf("%s: the recipe does not say %q:\n%s", mech, want, skill)
			}
		}
	}
}

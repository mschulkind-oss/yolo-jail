package jailcontent

import (
	"strings"
	"testing"
)

// THE `## Environment` BLOCK WAS FOUR HARDCODED LITERALS, AND THREE OF THEM WERE FALSE ON
// macos-user: `/workspace` (there is no bind mount and no such path), `/home/agent` (the
// account home is the sandbox user's), and "NixOS-based minimal container" (macOS, and no
// container at all). It contradicted the confinement header three lines above it, which had
// just been taught to say Seatbelt-not-namespaces.
//
// RULED 2026-09-13 — name the absence, keep `/workspace` canonical. The alternative readings
// were rejected on measured cost: substituting the real path everywhere needs templating the
// three built-in skills, which carry 25 `/workspace` references between them as STATIC
// markdown; and making `/workspace` real via /etc/synthetic.conf is a host-level mutation
// gated on an unmeasured Seatbelt question. So this bullet is the one place a macos-user
// agent learns that those 25 references mean its own path, which is why the ⚠ is asserted
// here and not merely the path.
func TestTheEnvironmentBlockDescribesTheBackendItActuallyRanOn(t *testing.T) {
	native := reachable(BriefingInput{
		Workspace: "/Users/Shared/yolo/proj",
		Mechanism: "macos-user",
		Home:      "/Users/_yolojail",
	})

	for _, want := range []string{
		"`/Users/Shared/yolo/proj` — the host directory itself",
		"There is no `/workspace` on this backend",
		"- **Home**: `/Users/_yolojail`",
		"macOS, Seatbelt-confined (no container",
	} {
		if !strings.Contains(native, want) {
			t.Errorf("the macos-user Environment block does not say %q.\n\nGot:\n%s",
				want, environmentBlockOf(t, native))
		}
	}

	// The three claims that were false. `/workspace` must not appear as this jail's
	// workspace path — the ⚠ names it, so a bare substring check would pass on the
	// warning alone; what must be gone is the CLAIM.
	for _, gone := range []string{
		"`/workspace` is the host directory",
		"bind-mounted LIVE",
		"- **Home**: `/home/agent`",
		"NixOS-based minimal container",
	} {
		if strings.Contains(native, gone) {
			t.Errorf("the macos-user Environment block still claims %q, which is false on a "+
				"backend that mounts nothing and is not Linux.\n\nGot:\n%s",
				gone, environmentBlockOf(t, native))
		}
	}
}

// The container answer is UNCHANGED, and that is half the ruling: `/workspace` stays
// canonical, so every jail that has one keeps the paragraph explaining the alias. An empty
// Mechanism means "the caller has not resolved a backend" and must render as a container.
func TestTheContainerEnvironmentBlockIsUntouched(t *testing.T) {
	for _, mech := range []string{"podman", "container", ""} {
		out := reachable(BriefingInput{Workspace: "/host/proj", Mechanism: mech})
		if !strings.Contains(out, "`/workspace` is the host directory `/host/proj`") {
			t.Errorf("mechanism %q lost the workspace alias paragraph:\n%s",
				mech, environmentBlockOf(t, out))
		}
		if !strings.Contains(out, "- **Home**: `/home/agent`") {
			t.Errorf("mechanism %q: Home must still default to /home/agent when the caller "+
				"supplies none:\n%s", mech, environmentBlockOf(t, out))
		}
		if strings.Contains(out, "There is no `/workspace`") {
			t.Errorf("mechanism %q got the native backend's absence warning:\n%s",
				mech, environmentBlockOf(t, out))
		}
	}
}

// environmentBlockOf returns just the `## Environment` section, so a failure quotes the
// thing under test rather than the whole briefing.
func environmentBlockOf(t *testing.T, briefing string) string {
	t.Helper()
	i := strings.Index(briefing, "## Environment")
	if i < 0 {
		return briefing
	}
	rest := briefing[i:]
	if j := strings.Index(rest[1:], "\n## "); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

func TestBriefingDelegatesLifecycleGuidanceToMechanicsSkill(t *testing.T) {
	for _, in := range []BriefingInput{
		{Workspace: "/Users/Shared/yolo/proj", Mechanism: "macos-user"},
		{Workspace: "/host/proj", Mechanism: "podman"},
	} {
		out := reachable(in)
		if !strings.Contains(out, "Read the **jail-mechanics** skill") {
			t.Error("briefing lacks the jail-mechanics pointer")
		}
		for _, stale := range []string{"## Packages", "container-limit change", "Please restart the jail"} {
			if strings.Contains(out, stale) {
				t.Errorf("briefing still includes moved lifecycle prose %q", stale)
			}
		}
	}
}

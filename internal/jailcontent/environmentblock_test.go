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
	native := BriefingContent(BriefingInput{
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
		out := BriefingContent(BriefingInput{Workspace: "/host/proj", Mechanism: mech})
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

// THE SAME DEFECT, ONE SECTION LOWER, AND THE 2026-09-13 FIX DID NOT REACH IT. `## Packages &
// Resource Limits` tells the agent to edit `resources` for a "container-limit change" — on a
// backend with no container, where no `resources` key is a kernel cap: `io` is a disk policy,
// `memory` a sampled guard, `cpus` parallelism defaults, and `pids_limit` is read and IGNORED
// ([DP-D1](../../docs/design/declaration-parity.md): RLIMIT_NPROC is per-USER and would collide
// across concurrent sessions on the shared account; "a cap a user believes in but that does not
// hold is worse than a documented absence"). TestTheMacosUserBriefingSaysWhatEachResourceKeyDoes
// pins what the section says of each key.
//
// So the instruction was worse than a wrong path: it invited the agent to ask the human for a
// limit that cannot be delivered, and the surrounding heading promised limits the launch does
// not impose. MEASURED in a real briefing on hardware 2026-09-13 — this section was still
// container-shaped in the file the sandbox actually read.
//
// ⚠ `/workspace` STAYS, and that is deliberate rather than an oversight: the 2026-09-13 ruling
// keeps it canonical and spends the Environment bullet above explaining that it means the real
// path. A second spelling here would fork the very convention that bullet exists to establish.
func TestThePackagesSectionDoesNotPromiseLimitsThisBackendIgnores(t *testing.T) {
	native := BriefingContent(BriefingInput{
		Workspace: "/Users/Shared/yolo/proj",
		Mechanism: "macos-user",
		Home:      "/Users/_yolojail",
	})

	for _, gone := range []string{
		"container-limit change",
		"(`packages` / `resources`)",
		"## Packages & Resource Limits",
	} {
		if strings.Contains(native, gone) {
			t.Errorf("the macos-user briefing still says %q, on a backend with no container "+
				"and no kernel-enforced resources.\n\nGot:\n%s", gone, packagesSectionOf(t, native))
		}
	}
	for _, want := range []string{
		"## Packages",
		"`/workspace/yolo-jail.jsonc`",
		"`packages`",
		// What the keys are NOT has to be named, by the same rule that governs every other
		// cell of this backend: an agent that reads `resources` and plans around a kernel cap
		// is the failure DP-D1's ruling describes.
		"`resources` is not enforced here",
	} {
		if !strings.Contains(native, want) {
			t.Errorf("the macos-user briefing does not say %q.\n\nGot:\n%s",
				want, packagesSectionOf(t, native))
		}
	}
}

// And the container answer is untouched — same half-ruling as the Environment block: a jail
// that HAS limits keeps being told how to change them.
func TestThePackagesSectionIsUntouchedOnAContainer(t *testing.T) {
	for _, mech := range []string{"podman", "container", ""} {
		out := BriefingContent(BriefingInput{Workspace: "/host/proj", Mechanism: mech})
		for _, want := range []string{
			"## Packages & Resource Limits",
			"container-limit change",
			"(`packages` / `resources`)",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("mechanism %q lost %q:\n%s", mech, want, packagesSectionOf(t, out))
			}
		}
		if strings.Contains(out, "not enforced here") {
			t.Errorf("mechanism %q got the native backend's absence line:\n%s",
				mech, packagesSectionOf(t, out))
		}
	}
}

// packagesSectionOf is environmentBlockOf's twin, for the section this pair tests.
func packagesSectionOf(t *testing.T, briefing string) string {
	t.Helper()
	i := strings.Index(briefing, "## Packages")
	if i < 0 {
		return briefing
	}
	rest := briefing[i:]
	if j := strings.Index(rest[1:], "\n## "); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

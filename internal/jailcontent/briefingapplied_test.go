package jailcontent

// The renderer half of "compose the briefing from what was APPLIED"
// (docs/design/backend-parity.md §6). BriefingInput now carries the mode the launch
// actually ran under beside the one the config asked for, and everything that describes
// networking reads the first.
//
// These pin the RENDER only. Which value arrives in AppliedNetMode, which limits reach
// Resources and which mounts survive into MountDescriptions are decisions of the run
// pipeline, and a test at this level cannot fail when that threading is deleted — those
// live in internal/cli/run/briefingapplied_test.go, which drives the real
// refreshJailBriefings and the real argv from one config.

import (
	"strings"
	"testing"
)

// networkLineOf returns the briefing's "- **Network**:" line.
func networkLineOf(t *testing.T, in BriefingInput) string {
	t.Helper()
	for _, line := range strings.Split(BriefingContent(in), "\n") {
		if strings.HasPrefix(line, "- **Network**:") {
			return line
		}
	}
	t.Fatalf("the briefing carries no network line at all")
	return ""
}

// The APPLIED mode wins over the configured one, in both directions — and they are
// genuinely opposed in each row, so neither can pass by reading whichever field the
// implementation happens to reach for.
//
// Row 1 is podman-in-podman: nesting forces --net=host whatever `network.mode` says, and
// the jail was reading "Bridge mode … `localhost` in here is the JAIL's loopback" while
// its loopback WAS the launcher's. Row 2 is Apple Container: it emits no network selector,
// so `network.mode: "host"` there produced "localhost resolves directly to the host" about
// a backend that had just warned it ignores the key.
func TestBriefingAppliedNetModeWinsOverTheConfiguredOne(t *testing.T) {
	forcedHost := networkLineOf(t, BriefingInput{
		Workspace: "/w", NetMode: "bridge", AppliedNetMode: "host",
	})
	if !strings.Contains(forcedHost, "Host networking") || strings.Contains(forcedHost, "Bridge mode") {
		t.Errorf("a jail forced onto host networking is told %q, want the host-networking line", forcedHost)
	}

	refusedHost := networkLineOf(t, BriefingInput{
		Workspace: "/w", NetMode: "host", AppliedNetMode: "bridge",
	})
	if !strings.Contains(refusedHost, "Bridge mode") || strings.Contains(refusedHost, "Host networking") {
		t.Errorf("a jail whose host mode was not honored is told %q, want the bridge line", refusedHost)
	}
}

// Without an applied mode the configured one still decides, and an empty input is still
// bridge. The fallback is what keeps every caller that has not resolved a backend —
// including the five tests that construct NetMode directly — rendering what it always did.
func TestBriefingFallsBackToNetModeWithoutAnAppliedMode(t *testing.T) {
	if line := networkLineOf(t, BriefingInput{Workspace: "/w", NetMode: "host"}); !strings.Contains(line, "Host networking") {
		t.Errorf("NetMode alone must still select the host paragraph; got %q", line)
	}
	if line := networkLineOf(t, BriefingInput{Workspace: "/w"}); !strings.Contains(line, "Bridge mode") {
		t.Errorf("an unset mode is bridge; got %q", line)
	}
}

// The port sections follow the applied mode too. Under host networking the stacks are
// shared and podman discards -p outright, so a nested jail carrying `network.ports` must
// not be handed a list of forwardings that are not happening — the same lie as the
// network line, one section down.
func TestBriefingAppliedHostModeSuppressesThePortSections(t *testing.T) {
	body := BriefingContent(BriefingInput{
		Workspace:        "/w",
		NetMode:          "bridge",
		AppliedNetMode:   "host",
		PublishPorts:     []any{"8080:80"},
		ForwardHostPorts: []any{"5432"},
	})
	for _, section := range []string{"Published Ports", "Forwarded Host Ports"} {
		if strings.Contains(body, section) {
			t.Errorf("%q is described under host networking, where neither key is honored:\n%s",
				section, body)
		}
	}
}

// Resources are rendered from whatever the caller says is applied, including limits the
// user never wrote. This is the Apple Container shape: no `resources` block at all, and
// the backend still caps memory and cpus — an agent believing it is uncapped while capped
// is the worse of the two lies, so the line appears. `pids_limit` is absent because that
// backend never passes it, which is the caller's decision (appliedResourceLimits) and
// arrives here simply as a key that is not in the map.
func TestBriefingStatesBackendDefaultResourceLimits(t *testing.T) {
	line := ""
	body := BriefingContent(BriefingInput{
		Workspace: "/w",
		Resources: map[string]any{"cpus": "4", "memory": "half of host RAM (min 4g)"},
	})
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "- **Resource limits**") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("limits the backend applies by default must still be stated:\n%s", body)
	}
	for _, want := range []string{"cpus=4", "memory=half of host RAM (min 4g)"} {
		if !strings.Contains(line, want) {
			t.Errorf("the resource line %q does not state %q", line, want)
		}
	}
	if strings.Contains(line, "pids_limit") {
		t.Errorf("the resource line names a key the caller did not apply: %q", line)
	}

	// And nothing applied means no line — the standing-bytes constraint the jail header
	// golden protects one section up.
	if body := BriefingContent(BriefingInput{Workspace: "/w"}); strings.Contains(body, "Resource limits") {
		t.Errorf("a jail with no applied limits gained a resource line:\n%s", body)
	}
}

// A backend that refused every context mount leaves no section behind. The caller filters
// MountDescriptions to what was actually bound; the renderer's contract is that an empty
// list produces no heading, rather than an "Additional Context Mounts (read-only)" section
// naming /ctx paths that do not exist in the jail.
func TestBriefingOmitsTheMountsSectionWhenNoneWereBound(t *testing.T) {
	bound := BriefingContent(BriefingInput{
		Workspace: "/w", MountDescriptions: []string{"/home/me/sysadmin:/ctx/sysadmin"},
	})
	if !strings.Contains(bound, "## Additional Context Mounts") || !strings.Contains(bound, "/ctx/sysadmin") {
		t.Errorf("a bound mount must be listed:\n%s", bound)
	}
	refused := BriefingContent(BriefingInput{Workspace: "/w"})
	if strings.Contains(refused, "## Additional Context Mounts") {
		t.Errorf("no bound mounts must leave no section:\n%s", refused)
	}
}

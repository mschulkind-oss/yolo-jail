package integration

import (
	"strings"
	"testing"
)

// DECLARATION PARITY'S macos-user ROWS, ASKED OF A REAL LAUNCH
// (docs/design/declaration-parity.md §5.1 and §11 step 7).
//
// THE GAP. The catalog's macos-user rows were each fixed in code and pinned on Linux, where the
// briefing is composed by jailcontent.BriefingContent and the launch lines by the run arm's
// printers. §11 step 7 is "everything gated on an instrument": no Mac has read the briefing that
// ARRIVES in the sandbox home, or the lines a real launch prints, for any of them. These two
// tests are that instrument for the rows a launch can answer, in the macos-user.yml job:
//
//   - TestMacosUserBriefingAndLaunchLinesDescribeThisBackend, one launch with the claude pack and
//     a workspace declaring `kvm`, `gpu.enabled`, `resources` and `ephemeral_storage`:
//   - DP-B19: the briefing's header is the native one, not "a sandboxed container";
//   - the Environment block ruled 2026-09-13 (§11's note): the real workspace path, "There is
//     no `/workspace` on this backend", and a macOS OS line instead of the container's;
//   - DP-B11: the Home line names one account home shared by every workspace;
//   - DP-B3: the network line is host networking, and nothing names host.containers.internal;
//   - DP-B6: no "Resource limits (kernel-enforced)" line and no yolo-cglimit offer, though the
//     workspace declares `resources`;
//   - DP-B4: the launch prints the `kvm` and `gpu.enabled` "not read on macos-user" lines;
//   - DP-B5: and the `ephemeral_storage: "tmpfs"` one, which this backend cannot give;
//   - the two refusals the profile makes, each with what gets past it: the unified log, which
//     the sandbox never reads and `yolo-log` reads on the host through the macos-log loophole,
//     and device ioctls on any /dev node `devices` does not list.
//   - TestMacosUserRefusesADeclaredContextMount: DP-B1, whose disposition since 2026-09-30 is
//     DP-D15's fatal refusal. A `mounts` entry whose source exists refuses the launch, naming it,
//     before any sandbox starts.
//
// DP-B8 (`yolo` resolvable by name in the sandbox) is TestMacosUserFooterSaysJail's
// renderer_on_path subtest already, and DP-B7 is the jail-daemon tests'.
func TestMacosUserBriefingAndLaunchLinesDescribeThisBackend(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{"kvm": true, "gpu": {"enabled": true}, "resources": {"memory": "2g"}, "ephemeral_storage": "tmpfs"}`)
	r := macosUserRunProbe(t, "declaration parity", ws, strings.Join([]string{
		`echo "=== BRIEFING ==="; cat ~/.claude/CLAUDE.md 2>&1`,
		`echo "=== END ==="`,
	}, "\n"))
	briefing := section(r.stdout, "=== BRIEFING ===", "=== END ===")
	if !strings.Contains(briefing, "## Environment") {
		t.Fatalf("no yolo briefing with an Environment block arrived at ~/.claude/CLAUDE.md, so "+
			"none of the briefing rows can be read:\n%s", lastLines(briefing, 30))
	}

	for _, c := range []struct{ row, want, why string }{
		{"DP-B19", "# YOLO Environment — jail (native, no container)",
			"the header is not the native one, so the agent is told something other than a Seatbelt sandbox on a real account"},
		{"Environment", "- **Workspace**: `" + ws + "` — the host directory itself",
			"the Workspace bullet does not name this workspace's real path"},
		{"Environment", "There is no `/workspace` on this backend",
			"the one sentence telling the agent that the built-in skills' `/workspace` means its own path is missing"},
		{"Environment", "- **OS**: macOS, Seatbelt-confined",
			"the OS line is not the macOS one"},
		{"DP-B11", "one account home, shared by every workspace on this",
			"the Home line does not say the account home is shared by every workspace"},
		{"DP-B3", "- **Network**: Host networking",
			"the network line is not host networking, which is what a native process has"},
		{"macos-log", "The macOS unified log is unreadable from this sandbox",
			"the agent is not told the sandbox cannot read the log, nor that the macos-log loophole's `yolo-log` reads it on the host"},
		{"devices", "Device control calls (`ioctl`) on /dev nodes are refused here",
			"the agent is not told the profile refuses device ioctls, nor that a `devices` entry is the fix"},
	} {
		if !strings.Contains(briefing, c.want) {
			t.Errorf("%s: the delivered briefing lacks %q: %s.\n%s", c.row, c.want, c.why, briefing)
		}
	}
	// DP-B6 refuses the OFFER of `yolo-cglimit`, not its name. The briefing's "What this
	// environment does NOT do for you" section names the client on purpose, to say it is not
	// available here, which is the opposite claim. This row refused the bare name until
	// 2026-10-02, and the first nightly to run it failed on exactly that sentence.
	// run.TestMacosUserDeclParityBriefingRowsHoldForTheComposedBriefing holds every row of
	// both tables against the Linux composition of this launch, so keep each row a positional
	// literal of string literals, `+` and `ws`.
	for _, c := range []struct{ row, unwanted, why string }{
		{"DP-B19", "a sandboxed container", "the briefing calls this backend a container"},
		{"Environment", "NixOS-based minimal container", "the container's OS line reached a macOS sandbox"},
		{"DP-B3", "host.containers.internal", "the agent is told to reach the host through a container hostname"},
		{"DP-B6", "Resource limits** (kernel-enforced)", "the agent is told `resources` are kernel-enforced on a backend that warns they are ignored"},
		{"DP-B6", "Sub-limit your own processes with `yolo-cglimit`", "the agent is offered a cgroup client with no delegate to talk to"},
	} {
		if strings.Contains(briefing, c.unwanted) {
			t.Errorf("%s: the delivered briefing contains %q: %s.\n%s", c.row, c.unwanted, c.why, briefing)
		}
	}

	out := r.combined()
	for _, want := range []string{
		"`kvm` is not read on macos-user",
		"`gpu.enabled` is not read on macos-user",
		"`ephemeral_storage: \"tmpfs\"` is not read on macos-user",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("DP-B4/DP-B5: the launch output lacks %q. The key is declared, this backend "+
				"reads none of them, and noteMacosUserPlatformGaps' line is the whole of what it "+
				"says about that.\n%s", want, out)
		}
	}
}

// TestMacosUserRefusesADeclaredContextMount is DP-B1's row as DP-D15 rules it: a `mounts` entry
// this backend cannot deliver refuses the launch (run.refuseMacosUserCtxMounts), and the refusal
// names the entry. The source is a directory this test minted, so the entry is one the refusal
// counts rather than one it skips as missing.
func TestMacosUserRefusesADeclaredContextMount(t *testing.T) {
	requireMacosUser(t)
	src := resolvedTempDir(t)
	packHome(t, `{"mounts": ["`+src+`"]}`)
	ws := macosUserWorkspace(t, `{}`)
	const marker = "=== SANDBOX STARTED ==="
	r := runMacosUser(t, ws, `echo "`+marker+`"`)
	out := r.combined()
	if r.rc == 0 || strings.Contains(r.stdout, marker) {
		t.Fatalf("DP-B1: a launch declaring the context mount %s STARTED (rc %d). This backend "+
			"cannot deliver one, and DP-D15 makes a declared one a refusal rather than an "+
			"absence the agent cannot see.\n%s", src, r.rc, out)
	}
	for _, want := range []string{"Refusing the macos-user launch", src} {
		if !strings.Contains(out, want) {
			t.Errorf("DP-B1: the refusal lacks %q, so it does not say which entry to remove:\n%s", want, out)
		}
	}
}

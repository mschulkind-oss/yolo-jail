package jailcontent

// briefingtopics.go renders the TOPIC FILES of the briefing diet (docs/reference/jail-mechanics-skill.md#topic-files,
// BD-D1): one Markdown file per subject of this jail, in the generated jail-mechanics skill's
// `topics/`, rendered from the same BriefingInput as the core. Every section the jail-managed body
// carried before the diet is here, verbatim or tightened; the core (briefingcore.go) keeps only
// the facts whose breach is silent, and points here.
//
// ONE RENDERER PER FACT. A topic is written to `topics/<file>` by the mechanics
// renderer when this entry stages the skill, and appended to the briefing itself when it does
// not (BriefingInput.InlineTopics); both read BriefingTopics, so the two can never say different
// things.
//
// A topic exists only when its subject does: no context mount, no mounts topic; no loophole,
// no loopholes topic. The host notch has none.

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/nixroots"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// BriefingTopic is one topic file (a term coined in docs/reference/agent-briefings.md#what-a-composed-briefing-contains).
type BriefingTopic struct {
	// File is the file name under the skill's `topics/`.
	File string
	// Name is the topic's word in the core's pointer and the skill's index.
	Name string
	// Title is the file's heading, and the section heading when the topic is inlined. It is the
	// heading the section had in the body before the diet.
	Title string
	// Body is the topic's lines, without the heading, ending with a blank line.
	Body []string
}

// Text is the topic file's content: its title as a top-level heading, then its body.
func (t BriefingTopic) Text() string {
	return strings.TrimRight(strings.Join(append([]string{"# " + t.Title, ""}, t.Body...), "\n"), "\n") + "\n"
}

// briefingView is the handful of values every part of a jail briefing derives from its input the
// same way: the applied network mode, the home, and the workspace at the jail's spelling.
type briefingView struct {
	netMode   string
	home      string
	workspace string
	native    bool
}

func newBriefingView(in BriefingInput) briefingView {
	// What the launch APPLIED wins over what the config asked for; NetMode is the fallback for a
	// caller that never resolved a backend. Everything downstream — the network line and both
	// port lists, which describe forwarding that only happens under bridge — keys off this one
	// value, so a nested jail forced onto host networking is told so instead of being told about
	// a bridge it does not have.
	v := briefingView{netMode: in.AppliedNetMode, home: in.Home, workspace: "/workspace"}
	if v.netMode == "" {
		v.netMode = in.NetMode
	}
	if v.netMode == "" {
		v.netMode = "bridge"
	}
	// macos-user has no /workspace: the workspace is the host directory itself. It has no network
	// namespace either: the launcher applies host networking to every native runtime
	// (appliedNetMode in internal/cli/run), and a caller that resolved no mode, or spelled it
	// `shared`, must not be told about a bridge and a gateway name the backend does not have. Nor
	// does it get a container's `/home/agent` when no home was applied.
	if slices.Contains(paths.NativeRuntimes, in.Mechanism) {
		v.native, v.workspace, v.netMode = true, in.Workspace, "host"
		return v
	}
	if v.netMode == "shared" {
		v.netMode = "host"
	}
	if v.home == "" {
		v.home = "/home/agent"
	}
	return v
}

// BriefingTopics renders this jail's topic files, in index order. It is empty at the host notch,
// whose body is its header alone.
func BriefingTopics(in BriefingInput) []BriefingTopic {
	if briefingSetup(in.Setup).Kind() == setup.KindHost {
		return nil
	}
	v := newBriefingView(in)
	topics := []BriefingTopic{
		{File: "storage.md", Name: "storage", Title: "Storage: the workspace, your home and what survives a restart", Body: storageTopic(in, v)},
		{File: "network.md", Name: "network", Title: "Network", Body: networkTopic(in, v)},
	}
	if len(in.ContextMounts) > 0 {
		topics = append(topics, BriefingTopic{File: "mounts.md", Name: "mounts", Title: "Additional Context Mounts", Body: mountsTopic(in)})
	}
	topics = append(topics, BriefingTopic{File: "host-requests.md", Name: "host requests", Title: "Running a command on the host", Body: hostRunSection()})
	if len(in.Loopholes) > 0 {
		topics = append(topics, BriefingTopic{File: "loopholes.md", Name: "loopholes", Title: "Loopholes — host capabilities wired into this jail", Body: loopholesTopic(in)})
	}
	if len(in.BlockedTools) > 0 {
		topics = append(topics, BriefingTopic{File: "blocked-tools.md", Name: "blocked tools", Title: "Blocked Tools", Body: blockedToolsTopic(in)})
	}
	return append(topics, BriefingTopic{File: "limits.md", Name: "limits", Title: "Limitations", Body: limitsTopic(in, v)})
}

// storageTopic is the Workspace and Home lines the Environment block carried, then the storage
// classes (persistencesection.go) under their own heading.
func storageTopic(in BriefingInput, v briefingView) []string {
	// THE WORKSPACE BULLET EXISTS TO EXPLAIN AN ALIAS, AND ON A NATIVE BACKEND THERE IS NONE. A
	// container binds the host directory at `/workspace`; a macos-user launch mounts nothing, the
	// agent is in the host directory at its real path, and `/workspace` does not exist at all.
	// RULED 2026-09-13: name the absence and keep `/workspace` canonical, so this bullet says
	// explicitly that references to `/workspace` mean the agent's own path.
	var lines []string
	if v.native {
		lines = append(lines,
			"- **Workspace**: `"+in.Workspace+"` — the host directory itself, not a",
			"  copy and not a mount. Edits here ARE the host's; there is never a git",
			"  pull/push, fetch, or any sync step for this directory.",
			"  ⚠ There is no `/workspace` on this backend. Skills and docs that name",
			"  `/workspace` — including the built-in ones — mean the path above.",
		)
		// Since slice 12a the home IS the workspace's sidecar in both modes
		// (macos-user-model-plan.md MMP-D25). A caller that applied no home gets no path.
		if v.home != "" {
			lines = append(lines,
				"- **Home**: `"+v.home+"` (this workspace's own; its links to shared logins and",
				"  caches reach state shared by every workspace on this machine)")
		}
		lines = append(lines, "")
		lines = append(lines, macosUserHomeLines...)
		lines = append(lines, "")
	} else {
		lines = append(lines,
			"- **Workspace**: `/workspace` is the host directory `"+in.Workspace+"`,",
			"  bind-mounted LIVE — the same files, not a copy. Host-side edits are",
			"  instantly visible here and vice versa; there is never a git",
			"  pull/push, fetch, or any sync step between the jail and the host",
			"  for this directory.",
			// NOT "(persistent across sessions)": on podman the home is a read-only bind, so the
			// claim invited writes that fail, and the agent fell back to /tmp
			// (docs/reference/jail-home.md#storage-classes-and-the-durable-dir).
			"- **Home**: `"+v.home+"`"+homeLineNote(in.Persistence, v.home),
			"",
		)
	}
	// The storage classes keep their own heading: a launch with neither a map nor a durable dir
	// has no section, as the body had none.
	return append(lines, persistenceSection(in.Persistence, in.Durable, v.workspace, in.Workspace, v.home)...)
}

// networkTopic is the network paragraph, the shared-network fact, and both port lists.
func networkTopic(in BriefingInput, v briefingView) []string {
	var networkLine string
	switch {
	case v.netMode == "host":
		// "this environment", not "the container": host networking is also what the macos-user
		// backend applies, and there is no container anywhere in it (DP-B3 / DP-L2).
		networkLine = "- **Network**: Host networking — this environment shares the host's network stack. `localhost` / `127.0.0.1` resolves directly to the host. No port mapping needed."
	case in.Sealed:
		// THE SEAL'S BRIDGE (FP-D13): a sealed launch never asks for the host's loopback, and
		// nothing of yolo's runs on the host for a build to reach. What the build has is the
		// network itself, for its dependencies.
		networkLine = "- **Network**: Bridge mode, on the runtime's own bridge. This is a sealed build jail " +
			"(a pack's build, handed no credential and no host file), so yolo asked for no forwarding of " +
			"the host's loopback (`$YOLO_HOST_LOOPBACK` is `unknown`), forwards and publishes no port, and " +
			"starts no host service. `localhost` in here is the JAIL's loopback. Outbound network works, " +
			"for fetching dependencies."
	default:
		// NO NUMERIC ADDRESS HERE: the address `host.containers.internal` resolves to is one of
		// three answers depending on the host stack (pasta, slirp4netns, gvproxy), so the line tells
		// the agent how to look instead of asserting one.
		//
		// The `unknown` clause is the macOS podman machine's: there the launcher excludes itself
		// from the decision and always says `unknown`, while gvproxy forwards the loopback anyway
		// (docs/plans/setup-support-gaps.md G8). It is said only where it holds (JM-P1), since the
		// diet put this text in a generated topic file.
		unknown := "and `unknown` means the launcher did not ask."
		if (in.Mechanism == "" || in.Mechanism == "podman") && in.IsMacOS {
			unknown = "and `unknown` means the launcher did not ask — on this macOS podman machine that is the normal " +
				"value and the hop usually works anyway, so probe it before concluding it does not."
		}
		networkLine = "- **Network**: Bridge mode. `localhost` in here is the JAIL's loopback. Reach the host at " +
			"`host.containers.internal` — including host services bound to the host's own `127.0.0.1`, which yolo " +
			"has the network stack forward in. Which ADDRESS that name resolves to depends on the host's network " +
			"stack, so read it (`getent hosts host.containers.internal`) rather than assuming one. " +
			"`$YOLO_HOST_LOOPBACK` says what the launcher decided: `requested`/`shared` mean forwarding is in " +
			"place, " + unknown
	}
	lines := []string{networkLine}
	// THE NETWORK PRIMITIVE'S FACT (OQ-NC3): a jail on a shared namespace keeps its autonomy, and
	// is told so beside the network line it qualifies. The sentence is render's, the one the launch
	// line prints, so the agent's copy and the human's cannot disagree.
	if fact := render.SharedNetworkFact(briefingProfile(in, v.netMode)); fact != "" {
		lines = append(lines, "- **Shared network**: "+fact)
	}
	lines = append(lines, publishedPortLines(in, v)...)
	lines = append(lines, forwardedPortLines(in, v)...)
	return append(lines, "")
}

// publishedPortLines and forwardedPortLines are the two DIRECTIONS, rendered separately on
// purpose: their entry orders are opposite (network.ports is "HOST:JAIL", forward_host_ports
// "JAIL:HOST"), so neither may be rendered as a bare pair. Both are suppressed under host
// networking, where neither key is honored at launch.
func publishedPortLines(in BriefingInput, v briefingView) []string {
	if len(in.PublishPorts) == 0 || v.netMode == "host" {
		return nil
	}
	lines := []string{"- **Published Ports** (the HOST connects IN to a server you run in here). Only works if " +
		"the server binds `0.0.0.0`; a `127.0.0.1` listener in here is not publishable:"}
	for _, entry := range in.PublishPorts {
		hp, jp, ok := publishEntry(entry)
		if !ok {
			continue
		}
		lines = append(lines, "  - jail port "+jp+" → `localhost:"+hp+"` on the host")
	}
	return lines
}

func forwardedPortLines(in BriefingInput, v briefingView) []string {
	if len(in.ForwardHostPorts) == 0 || v.netMode == "host" {
		return nil
	}
	lines := []string{"- **Forwarded Host Ports** (YOU connect OUT to a service on the host). These answer on " +
		"the JAIL's own `localhost`, so a client in here can use `localhost:<port>` directly:"}
	for _, entry := range in.ForwardHostPorts {
		lp, hp, kind := portEntry(entry)
		switch kind {
		case portInt, portPlain:
			lines = append(lines, "  - `localhost:"+lp+"` in here → host port "+lp)
		case portMapped:
			lines = append(lines, "  - `localhost:"+lp+"` in here → host port "+hp)
		}
	}
	return lines
}

// mountsTopic lists the context mounts this launch delivered. THE MODE IS PER ENTRY: a
// read-write config element is the host's own directory, and an agent told it was a read-only
// view would treat a write there as scratch (docs/reference/context-mounts.md §3.8). The path is
// the one the agent opens on THIS backend, and the context dir's variable is named so pack prose
// spelling `$YOLO_CONTEXT_DIR/<rel>` resolves.
func mountsTopic(in BriefingInput) []string {
	ctxDir := in.ContextDir
	if ctxDir == "" {
		ctxDir = paths.ContainerContextDir
	}
	lines := []string{"Host directories mounted into this jail. `$" + paths.ContextDirEnv + "` is `" + ctxDir + "` here.", ""}
	anyRW, anyLinked, anyCopied := false, false, false
	for _, m := range in.ContextMounts {
		mode := "read-only"
		if m.ReadWrite {
			mode, anyRW = "read-write", true
		}
		// A COPY IS A SNAPSHOT: the bytes it opens are the host file as it was at launch.
		if m.Copied {
			mode, anyCopied = mode+"; copied at launch; host edits arrive at the next launch", true
		} else {
			anyLinked = true
		}
		// AN OPTIONAL MOUNT MAY BE ABSENT: a check that runs after the briefing is written (the
		// macos-user sandbox account's DAC preflight) may still skip it.
		if m.Optional {
			mode = "optional; may be absent; " + mode
		}
		entry := "- `" + m.Path + "` (" + mode + "; host `" + m.Host + "`"
		if m.Pack != "" {
			entry += "; from pack `" + m.Pack + "`"
		}
		lines = append(lines, entry+")")
	}
	if anyRW {
		lines = append(lines, "",
			"A read-write mount is the host's own directory, not a copy: what you write there",
			"is what the host reads.")
	}
	// THE LINK DELIVERY'S TWO DELTAS (docs/reference/context-mounts.md#what-cannot-be-matched):
	// with no container there is no mount namespace, so each entry is a link to the host folder
	// itself. A copied entry is not a link, so the sentence leaves it out by name.
	if MechanismHasNoContainer(in.Mechanism) && anyLinked {
		each := "Each is a link"
		if anyCopied {
			each = "Each one not copied is a link"
		}
		lines = append(lines, "",
			each+" to the host folder itself, not a mount: `pwd -P`, `realpath` and",
			"git print the host path, and a subfolder this account may not read stays",
			"unreadable here (`Permission denied`).")
	}
	return append(lines, "")
}

// loopholesTopic lists each active loophole's first sentence, and what it holds in this project.
func loopholesTopic(in BriefingInput) []string {
	var lines []string
	for _, lh := range in.Loopholes {
		first := loopholeFirst(lh.Desc)
		if first != "" {
			lines = append(lines, "- **"+lh.Name+"**: "+first)
		} else {
			lines = append(lines, "- **"+lh.Name+"**")
		}
		// What it holds in this project, so an agent does not debug a socket that was never bound
		// (docs/reference/loophole-grants.md §5).
		if lh.Standing != "" {
			lines = append(lines, "  - "+lh.Standing)
		}
	}
	return append(lines, "", "Details: `yolo loopholes list`.", "")
}

// blockedToolsTopic is each block's message and suggestion, and the `rg -r` warning when a block
// names `rg` as its replacement (BD-D7): before the diet the warning was unconditional, and a jail
// with no ripgrep block was warned about a flag it had no reason to reach for.
func blockedToolsTopic(in BriefingInput) []string {
	lines := []string{"The following tools are blocked or shimmed in this project:", ""}
	for _, tool := range in.BlockedTools {
		entry := "- `" + tool.Name + "`"
		if tool.Message != "" {
			entry += ": " + tool.Message
		}
		if tool.Suggestion != "" {
			entry += " Use `" + tool.Suggestion + "` instead."
		}
		lines = append(lines, entry)
	}
	lines = append(lines, "")
	if blocksInFavourOfRg(in.BlockedTools) {
		lines = append(lines,
			"⚠ rg is recursive by default — never pass grep-style `-r`/`-rn` flags",
			"(in rg, `-r` means `--replace` and silently corrupts match output).",
			"Use `rg -n <pattern> [path]`.",
			"",
		)
	}
	return lines
}

func blocksInFavourOfRg(tools []BlockedTool) bool {
	for _, t := range tools {
		if t.Replacement == "rg" {
			return true
		}
	}
	return false
}

// limitsTopic is what the jail is not and does not do: the backend's standing constraints, the
// OS, the credential boundary, the resource limits, the disk I/O priority and the host nix
// daemon's GC-root rule.
func limitsTopic(in BriefingInput, v briefingView) []string {
	var lines []string
	if v.native {
		lines = append(lines, "- **OS**: macOS, Seatbelt-confined (no container, no systemd, no sudo)")
	} else {
		lines = append(lines, "- **OS**: NixOS-based minimal container (no systemd, no sudo)")
	}
	lines = append(lines,
		"- Host credentials are not propagated into the jail: the host's `~/.ssh`,",
		"  `~/.gitconfig`, and cloud/gh tokens are invisible here. This is a credential",
		"  boundary, not a network block — outbound SSH and HTTPS work normally, so git",
		"  push/pull and API calls succeed whenever the jail has its own credentials",
		"  (e.g. a workspace-specific deploy key or a token in `.env`), or when a pack",
		"  says `git` or `gh` runs through the host's own credentials. Without",
		"  either, authenticated operations fail.",
	)
	// DP-B1's second briefing site: the `/ctx` clause appears exactly when the mounts topic does.
	if len(in.ContextMounts) > 0 {
		lines = append(lines, "- No sudo/root; context mounts under `$"+paths.ContextDirEnv+
			"` are read-only unless marked read-write.")
	} else {
		lines = append(lines, "- No sudo/root.")
	}
	if len(in.Resources) > 0 {
		keys := make([]string, 0, len(in.Resources))
		for k := range in.Resources {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+pyValue(in.Resources[k]))
		}
		lines = append(lines, "- **Resource limits** (kernel-enforced): "+strings.Join(parts, ", ")+
			".  Sub-limit your own processes with `yolo-cglimit` (`--help` for usage).")
	}
	lines = append(lines, ioPriorityLines(in)...)
	if in.HostNix {
		lines = append(lines,
			"- **Nix** uses the host's daemon and store (`NIX_REMOTE=daemon`). A `result`",
			"  link, `--out-link`, `nix profile` or `.direnv` root made under the workspace or",
			fmt.Sprintf("  your home is kept for the host by a yolo-owned copy, for %d days after its last",
				int(nixroots.DefaultLease/(24*time.Hour))),
			fmt.Sprintf("  build and at most %d per workspace: `yolo nix-roots list` shows them, `keep",
				nixroots.DefaultCap),
			"  <link>` renews one, `release` gives one back. A link under /tmp is NOT a GC",
			"  root the host honors. If a link dangles anyway, rebuild it.",
		)
	}
	lines = append(lines, "")
	// The standing constraints of this backend, in the agent's voice (run.backendLimits), under
	// the heading the body gave them: last, so the bullets above stay under the topic's title.
	if len(in.BackendLimits) > 0 {
		lines = append(lines, "## What this environment does NOT do for you", "")
		for _, l := range in.BackendLimits {
			lines = append(lines, "- "+l)
		}
		lines = append(lines, "")
	}
	return lines
}

// ioPriorityLines report what was emitted (backend-parity.md §6): the class the launch passed, in
// words that claim neither enforcement nor effect. On macos-user the mechanism is the launcher's
// macOS disk I/O policy, inherited by the session (docs/design/io-priority.md §5.5).
func ioPriorityLines(in BriefingInput) []string {
	p := ioprio.Priority(in.IOPriority)
	if !p.Declared() {
		return nil
	}
	if slices.Contains(paths.NativeRuntimes, in.Mechanism) {
		pol, _ := p.DarwinPolicy()
		return []string{
			"- **Disk I/O priority**: `" + in.IOPriority + "` (" + ioprio.DarwinPolicyName(pol) + "), " +
				"the macOS disk I/O policy the launcher set before this session started, so every " +
				"process here inherits it and builds yield the disk under contention. Advisory, not a " +
				"limit: a process can set its own, and work a host process does for the session is " +
				"outside it: a `nix build` through the host nix daemon keeps the host's policy.",
		}
	}
	return []string{
		"- **Disk I/O priority**: `" + in.IOPriority + "` (" + p.ClassName() + "), set on every " +
			"process in this jail at boot so builds here yield the disk under contention. " +
			"Advisory, not a limit: a process can raise its own, it does not reach buffered " +
			"writeback, disks whose scheduler is " + joinOr(ioprio.IgnoredBy(p)) + " ignore it, " +
			"and work a host process does for the jail is outside it: a `nix build` through the " +
			"host nix daemon keeps the host's priority.",
	}
}

// inlineSkillsSection is the Skills section the body carried before the diet, appended only when
// the topics are inlined: with the skill staged, the index says the same.
func inlineSkillsSection(in BriefingInput) []string {
	return []string{"## Skills", "",
		"User-level skills dirs (`~/.<agent>/skills/`) are **read-only** in-jail",
		"(kernel-enforced); workspace-level ones (`/workspace/.<agent>/skills/`) are",
		"writable — develop there, then ask the human to promote to the host.",
		"",
		"On-demand skills are staged for you: read **" + BuiltinSkillName(in.ShippedSkillPrefix, "configuring-the-jail") + "** before",
		"editing `" + in.configName() + "`, and **" + BuiltinSkillName(in.ShippedSkillPrefix, "diagnosing-the-jail") + "** when a command",
		"misbehaves. Their bodies load only when invoked — they cost nothing until then.",
		"",
		toolingLine,
		"",
	}
}

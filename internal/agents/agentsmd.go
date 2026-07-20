package agents

// Per-workspace AGENTS.md / CLAUDE.md briefing generation and host-skill
// staging. The briefing content is a byte-exact string contract;
// WriteBriefing's hardlink-breaking truncation is an inode-preservation
// contract a running jail's bind mount depends on.

import (
	"bytes"
	"os"
	"sort"
	"strings"
)

// BlockedTool is one entry of the "Blocked Tools" section (name + optional
// message + optional suggestion).
type BlockedTool struct {
	Name       string
	Message    string
	Suggestion string
}

// Loophole is a (name, description) pair for the loopholes section.
type Loophole struct {
	Name string
	Desc string
}

// BriefingInput carries everything generate_agents_md's jail-managed content
// depends on. Workspace is the host workspace path (rendered verbatim);
// ProvisioningFailed is true when the last boot's .yolo/startup.log contained
// "PROVISIONING FAILED" (the caller reads the log — see ReadProvisioningFailed).
type BriefingInput struct {
	Workspace          string
	BlockedTools       []BlockedTool
	MountDescriptions  []string
	NetMode            string
	ForwardHostPorts   []any
	Loopholes          []Loophole
	Resources          map[string]any
	IsYoloSourceTree   bool
	ProvisioningFailed bool
}

// BriefingContent renders the jail-managed briefing body (before any host-level
// user content is prepended and before agents_md_extra is appended). This is
// the byte-exact string generate_agents_md builds via its `lines` list joined
// with "\n" plus a trailing newline. NetMode defaults to "bridge" when empty.
func BriefingContent(in BriefingInput) string {
	netMode := in.NetMode
	if netMode == "" {
		netMode = "bridge"
	}

	var networkLine string
	if netMode == "host" {
		networkLine = "- **Network**: Host networking — the container shares the host network stack. `localhost` / `127.0.0.1` resolves directly to the host. No port mapping needed."
	} else {
		networkLine = "- **Network**: Bridge mode. Use `host.containers.internal` (resolves to 169.254.1.2) to reach the host."
	}

	var forwardedPorts []string
	if len(in.ForwardHostPorts) > 0 && netMode != "host" {
		forwardedPorts = append(forwardedPorts,
			"- **Forwarded Host Ports**: The following host services are available on `localhost` inside this container:")
		for _, entry := range in.ForwardHostPorts {
			lp, hp, kind := portEntry(entry)
			switch kind {
			case portInt:
				forwardedPorts = append(forwardedPorts, "  - `localhost:"+lp+"` → host port "+lp)
			case portMapped:
				forwardedPorts = append(forwardedPorts, "  - `localhost:"+lp+"` → host port "+hp)
			case portPlain:
				forwardedPorts = append(forwardedPorts, "  - `localhost:"+lp+"` → host port "+lp)
			}
		}
	}

	var resourceLine []string
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
		resourceLine = []string{
			"- **Resource limits** (kernel-enforced): " + strings.Join(parts, ", ") +
				".  Sub-limit your own processes with `yolo-cglimit` (`--help` for usage).",
		}
	}

	var provisioningFailed []string
	if in.ProvisioningFailed {
		provisioningFailed = []string{
			"## ⚠ Provisioning failed",
			"",
			"The last boot's provisioning failed — project tools may be missing.",
			"Read `/workspace/.yolo/startup.log` and self-serve (e.g. run",
			"`mise install` in /workspace, then re-run the step that failed).",
			"",
		}
	}

	lines := []string{
		"# YOLO Jail Environment",
		"",
		"You are running inside a YOLO Jail — a sandboxed container.",
		"Jail tooling: `yolo --help`; config reference: `yolo config-ref`.",
		"",
	}
	lines = append(lines, provisioningFailed...)
	lines = append(lines,
		"## Environment",
		"",
		"- **Workspace**: `/workspace` is the host directory `"+in.Workspace+"`,",
		"  bind-mounted LIVE — the same files, not a copy. Host-side edits are",
		"  instantly visible here and vice versa; there is never a git",
		"  pull/push, fetch, or any sync step between the jail and the host",
		"  for this directory.",
		"- **Home**: `/home/agent` (persistent across sessions)",
		"- **OS**: NixOS-based minimal container (no systemd, no sudo)",
		networkLine,
	)
	lines = append(lines, forwardedPorts...)
	lines = append(lines, resourceLine...)
	lines = append(lines,
		"",
		"⚠ rg is recursive by default — never pass grep-style `-r`/`-rn` flags",
		"(in rg, `-r` means `--replace` and silently corrupts match output).",
		"Use `rg -n <pattern> [path]`.",
		"",
	)

	if len(in.Loopholes) > 0 {
		lines = append(lines, "## Loopholes — host capabilities wired into this jail", "")
		for _, lh := range in.Loopholes {
			first := loopholeFirst(lh.Desc)
			if first != "" {
				lines = append(lines, "- **"+lh.Name+"**: "+first)
			} else {
				lines = append(lines, "- **"+lh.Name+"**")
			}
		}
		lines = append(lines, "", "Details: `yolo loopholes list`.", "")
	}

	if len(in.BlockedTools) > 0 {
		lines = append(lines,
			"## Blocked Tools",
			"",
			"The following tools are blocked or shimmed in this project:",
			"",
		)
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
	}

	if len(in.MountDescriptions) > 0 {
		lines = append(lines, "## Additional Context Mounts (read-only)", "")
		for _, m := range in.MountDescriptions {
			hostPath, containerPath := m, m
			if i := strings.Index(m, ":"); i >= 0 {
				hostPath, containerPath = m[:i], m[i+1:]
			}
			lines = append(lines, "- `"+containerPath+"` (from host `"+hostPath+"`)")
		}
		lines = append(lines, "")
	}

	lines = append(lines,
		"## Limitations",
		"",
		"- Host credentials are not propagated into the jail: the host's `~/.ssh`,",
		"  `~/.gitconfig`, and cloud/gh tokens are invisible here. This is a credential",
		"  boundary, not a network block — outbound SSH and HTTPS work normally, so git",
		"  push/pull and API calls succeed whenever the jail has its own credentials",
		"  (e.g. a workspace-specific deploy key or a token in `.env`). Only without",
		"  such jail-local credentials do authenticated operations fail.",
		"- No sudo/root; context mounts under `/ctx/` are read-only.",
		"",
		"## Packages & Resource Limits",
		"",
		"To request a tool or a container-limit change: edit `/workspace/yolo-jail.jsonc`",
		"(`packages` / `resources`), ALWAYS run `yolo check` after every config edit",
		"(`yolo check --no-build` is fine inside a running jail), then ask the human to",
		"restart the jail. Reference: `yolo config-ref`.",
		"",
		"## Skills",
		"",
		"User-level skills dirs (`~/.<agent>/skills/`) are **read-only** in-jail",
		"(kernel-enforced); workspace-level ones (`/workspace/.<agent>/skills/`) are",
		"writable — develop there, then ask the human to promote to the host.",
		"",
	)

	if in.IsYoloSourceTree {
		lines = append(lines,
			"## Testing Changes to yolo-jail",
			"",
			"The `/workspace` directory is a bind mount of the host's repo, and it also",
			"backs `/opt/yolo-jail` — so nested jails launched from here run your edited",
			"Go code live (via dev-override wrappers that prefer `/opt/yolo-jail/dist-go/`).",
			"",
			"When modifying `cmd/` or `internal/`, **always verify with a nested",
			"jail** before telling the human to test on the host. Run `yolo -- bash` from",
			"inside this jail to launch one and confirm your changes work end-to-end.",
			"Container startup errors (mount failures, permission errors, read-only",
			"filesystem conflicts) are only caught by actually running the container —",
			"unit tests alone are not sufficient.",
			"",
			"**Important:** Run `just deploy` to cross-compile Go binaries. Changes to",
			"`flake.nix` require `just load && just install` on the host since the image",
			"is baked by Nix.",
			"",
		)
	}

	return strings.Join(lines, "\n") + "\n"
}

// ComposeBriefing appends agents_md_extra to the jail content the way
// generate_agents_md does: jail_content + "\n" + extra.rstrip() + "\n" when
// extra is non-empty.
func ComposeBriefing(jailContent, extra string) string {
	if extra == "" {
		return jailContent
	}
	return jailContent + "\n" + strings.TrimRight(extra, " \t\r\n") + "\n"
}

// PrependHostBriefing produces one agent's final briefing: the host briefing
// file's content + "\n---\n\n" + jailContent when the host file exists, else
// jailContent alone.
func PrependHostBriefing(hostBriefingPath, jailContent string) string {
	data, err := os.ReadFile(hostBriefingPath)
	if err != nil {
		return jailContent
	}
	return string(data) + "\n---\n\n" + jailContent
}

type portKind int

const (
	portNone portKind = iota
	portInt
	portMapped
	portPlain
)

// portEntry classifies a forward_host_ports entry the way generate_agents_md's
// isinstance ladder does, returning the rendered local/host port strings. An
// int → (n, n, portInt); a string "a:b" → (a, b, portMapped) [split once]; a
// plain string → (s, s, portPlain); anything else → portNone.
func portEntry(entry any) (local, host string, kind portKind) {
	// jsonx decodes ints as jsonInt; accept both that and native ints.
	if s, ok := intString(entry); ok {
		return s, s, portInt
	}
	if str, ok := entry.(string); ok {
		if i := strings.Index(str, ":"); i >= 0 {
			return str[:i], str[i+1:], portMapped
		}
		return str, str, portPlain
	}
	return "", "", portNone
}

// loopholeFirst extracts the first-sentence summary of a loophole description,
// mirroring: (desc or "").split(". ")[0].split("\n")[0].strip().rstrip(".").
func loopholeFirst(desc string) string {
	s := desc
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	return strings.TrimRight(s, ".")
}

// WorkspaceIsYoloSourceTree reports whether workspace is a yolo-jail source
// checkout: go.mod with the yolo-jail module path AND cmd/yolo/main.go present.
func WorkspaceIsYoloSourceTree(workspace string) bool {
	data, err := os.ReadFile(workspace + "/go.mod")
	if err != nil {
		return false
	}
	if !bytes.Contains(data, []byte("yolo-jail")) {
		return false
	}
	_, err = os.Stat(workspace + "/cmd/yolo/main.go")
	return err == nil
}

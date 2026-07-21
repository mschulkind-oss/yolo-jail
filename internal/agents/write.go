package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// BuiltinJailStartupSkill is the built-in jail-startup skill written into every
// agent's staging dir.
const BuiltinJailStartupSkill = `---
name: jail-startup
description: First-run skill for agents entering a YOLO Jail. Reads the handover document left by the outer agent and orients you to the jail environment. Invoke this skill immediately when starting a new session inside a jail.
---

# Jail Startup

You are running inside a **YOLO Jail** — an isolated container environment.
This skill helps you pick up where the previous (outer) agent left off.

## Step 1: Read the Handover Document

The outer agent was REQUIRED to write a handover document before you were
launched. Read it now:

**Primary location:** ` + "`.yolo/handover.md`" + ` (i.e., ` + "`/workspace/.yolo/handover.md`" + `)

If it exists, read it carefully — it contains:
- What the outer agent was working on
- What remains to be done
- Key decisions and rationale
- Files to look at first
- Gotchas and context you need

If the file does NOT exist, tell the human:
> "No handover document found at ` + "`.yolo/handover.md`" + `. The outer agent should
> have created one. Can you tell me what I should be working on?"

## Step 2: Orient Yourself

Key facts about your environment:
- **Workspace** is at ` + "`/workspace`" + ` — this is the SAME directory as on the host (bind-mounted read-write). Changes you make are immediately visible on the host.
- **Internet** is available. You can curl, pip install, npm install, etc.
- **Home** is ` + "`/home/agent`" + ` — shared across ALL jail workspaces. Auth tokens, tool caches, and configs persist here.
- **Tools**: git, rg, fd, bat, jq, nvim, curl, gh, uv, mise, tmux, and more.
- **Runtimes**: Node.js, Python, Go (managed by mise).
- **Blocked tools**: Some tools may be shimmed (e.g., grep → rg). Check AGENTS.md or run ` + "`ls ~/.yolo-shims/`" + ` if you hit unexpected blocks. Set ` + "`YOLO_BYPASS_SHIMS=1`" + ` for scripts that need originals.
- **No pagers**: ` + "`PAGER=cat`" + `. Never pipe to ` + "`less`" + ` or ` + "`more`" + `.
- Run ` + "`yolo config-ref`" + ` for full configuration and environment reference.

## Step 3: Execute

After reading the handover document, proceed with the tasks described in it.
You have full capability — treat this as your primary working environment.
`

// WriteBriefing writes content to path, truncating in place to preserve the
// inode a running jail's bind mount captured — EXCEPT when the file is
// multi-linked (st_nlink > 1, e.g. after a `yolo prune` hardlink-dedup), in
// which case it unlinks first so a fresh inode is allocated (breaking the link
// rather than clobbering every fused sibling).
func WriteBriefing(path, content string) error {
	if fi, err := os.Lstat(path); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			_ = os.Remove(path) // best-effort: ignore removal errors
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ReadProvisioningFailed reports whether workspace/.yolo/startup.log exists and
// contains "PROVISIONING FAILED". A read error → false.
func ReadProvisioningFailed(workspace string) bool {
	data, err := os.ReadFile(filepath.Join(workspace, ".yolo", "startup.log"))
	if err != nil {
		return false
	}
	return containsSub(string(data), "PROVISIONING FAILED")
}

func containsSub(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}

// intString returns the base-10 string of v when v is an integer value (a
// jsonx-decoded int or a native Go int/int64) — used to classify a
// forward_host_ports entry.
func intString(v any) (string, bool) {
	if jsonx.IsInt(v) {
		n, _ := jsonx.AsInt(v)
		return strconv.FormatInt(n, 10), true
	}
	switch n := v.(type) {
	case int:
		return strconv.Itoa(n), true
	case int64:
		return strconv.FormatInt(n, 10), true
	}
	return "", false
}

// pyValue renders a resources map value as it appears in the briefing: strings
// verbatim; ints without ".0"; anything else via a plain format.
func pyValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if s, ok := intString(v); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

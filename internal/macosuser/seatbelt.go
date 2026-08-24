package macosuser

import (
	"path"
	"strings"
)

// SeatbeltProfile generates the SBPL sandbox profile, matching SandVault's
// structure: (allow default) with targeted denies, last-match-wins so re-allows
// follow their denies.
//
// The workspace + sandbox-home paths are SBPL-escaped via sbplStr. sandboxHome
// defaults to SandboxHome() when empty.
//
// readonlyRels carries config.workspace_readonly — workspace-relative sub-paths
// the agent must not write. Each becomes one `(deny file-write* (subpath …))`
// emitted AFTER the writable-set allow, which is what makes it stick: SBPL is
// last-match-wins, and this profile depends on that twice already (the
// deny-`/`-then-allow that gives the agent any write at all, and the `/Users`
// read-deny followed by re-allowed literals). Nothing later in the profile
// re-allows file-write*, so the denies are terminal — verified by
// TestSeatbeltProfileHasNoWriteAllowAfterReadonlyDenies.
//
// Why this exists: the key is delivered as a `-v …:ro` bind on the container
// backends (internal/cli/run/mounts.go), and macos-user has no mounts, so it
// used to accept the key and silently do nothing. A security key that lies is
// worse than one that refuses — the config reads as protection that is not
// there. See docs/design/host-execution-from-the-workspace.md §5.5, §5.6 item 1.
//
// Entries that are absolute or escape the workspace are dropped rather than
// emitted; config validation already rejects both (internal/config/validate.go
// validateWorkspaceReadonly), so this is defence in depth against a caller that
// skipped it, not a second error channel.
func SeatbeltProfile(workspace, sandboxHome string, readonlyRels []string) string {
	if sandboxHome == "" {
		sandboxHome = SandboxHome()
	}
	ws := sbplStr(workspace)
	home := sbplStr(sandboxHome)
	ancestors := ancestorLiterals(workspace, sandboxHome)
	return "(version 1)\n" +
		";; yolo-jail macOS-user sandbox profile — SandVault-parity.\n" +
		";; Base allow with targeted denies; last match wins.\n" +
		"(allow default)\n" +
		"\n" +
		";; --- Writes: deny everywhere, then re-allow the agent's writable set ---\n" +
		"(deny file-write* (subpath \"/\"))\n" +
		"(allow file-write*\n" +
		"    (subpath " + ws + ")\n" +
		"    (subpath " + home + ")\n" +
		"    (subpath \"/tmp\")\n" +
		"    (subpath \"/private/tmp\")\n" +
		"    (subpath \"/var/folders\")\n" +
		"    (subpath \"/private/var/folders\")\n" +
		"    (subpath \"/dev\"))\n" +
		readonlyDenies(workspace, readonlyRels) +
		"\n" +
		";; --- Volumes: deny reads except the boot volume ---\n" +
		"(deny file-read* (subpath \"/Volumes\"))\n" +
		"(allow file-read* (subpath \"/Volumes/Macintosh HD\"))\n" +
		"\n" +
		";; --- Raw disk + packet capture: never ---\n" +
		"(deny file-read* file-write*\n" +
		"    (regex #\"^/dev/r?disk\")\n" +
		"    (regex #\"^/private/dev/r?disk\")\n" +
		"    (regex #\"^/dev/bpf\"))\n" +
		"\n" +
		";; --- Other users' homes: deny reads under /Users, re-allow the traversal\n" +
		";;     entries + the (neutral, non-home) workspace + this sandbox user's own\n" +
		";;     home.  Every INTERMEDIATE dir of the workspace is granted as a\n" +
		";;     (literal) too: tools that walk up to a repo boundary stat the whole\n" +
		";;     chain, and (literal) grants the dir entry WITHOUT re-allowing the\n" +
		";;     siblings a (subpath) would. ---\n" +
		"(deny file-read* (subpath \"/Users\"))\n" +
		"(allow file-read*\n" +
		"    (literal \"/Users\")\n" +
		"    (literal \"/Users/Shared\")\n" +
		ancestors +
		"    (subpath " + ws + ")\n" +
		"    (subpath " + home + "))\n" +
		"\n" +
		";; --- Keychains: System.keychain is world-readable (0644) on stock\n" +
		";;     macOS, so this deny is load-bearing ---\n" +
		"(deny file-read* (subpath \"/Library/Keychains\"))\n" +
		"\n" +
		";; --- Process introspection the agent's tooling needs ---\n" +
		"(allow process-info*)\n" +
		"(allow sysctl-read)\n"
}

// readonlyDenies renders the config.workspace_readonly block: ONE
// `(deny file-write* …)` form carrying one `(subpath "<ws>/<rel>")` clause per
// entry, or "" when there are none, so a profile without the key is
// byte-identical to the one this backend emitted before the key was wired.
//
// The "one deny per entry" spelling this comment carried until 2026-08-23 was
// wrong, and it had already been copied into
// docs/research/macos-support-matrix.md before anyone read the body.
//
// Placed immediately after the writable-set allow rather than at the end of the
// profile: both positions are correct (nothing later re-allows file-write*), and
// keeping the whole write policy — deny all, allow the agent's set, re-deny the
// carve-outs — readable as one unit is worth more than the freedom to append.
func readonlyDenies(workspace string, rels []string) string {
	if len(rels) == 0 {
		return ""
	}
	var b strings.Builder
	for _, rel := range rels {
		rel = strings.TrimSpace(rel)
		// Absolute or escaping entries are config errors caught upstream; drop
		// them here rather than emitting a deny on a path outside the workspace,
		// which would silently widen the profile instead of narrowing it.
		if rel == "" || strings.HasPrefix(rel, "/") || rel == ".." ||
			strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") ||
			strings.HasSuffix(rel, "/..") {
			continue
		}
		b.WriteString("    (subpath " + sbplStr(path.Join(workspace, rel)) + ")\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n" +
		";; --- config.workspace_readonly: host-executed paths the agent must not\n" +
		";;     write.  Must follow the allow above — last match wins. ---\n" +
		"(deny file-write*\n" + strings.TrimSuffix(b.String(), "\n") + ")\n"
}

// ancestorLiterals renders one `(literal "…")` line per INTERMEDIATE directory of
// the given paths that sits under the /Users deny — i.e. strictly between
// /Users/Shared and the path itself.
//
// Why this exists. `(deny file-read* (subpath "/Users"))` denies the chain, and
// re-allowing only /Users, /Users/Shared and the workspace SUBPATH leaves a hole
// at every level in between. At depth one (/Users/Shared/proj) there is no
// in-between and the old three grants were complete, which is why the gap
// survived: the shipped test used /Users/Shared/proj. A real workspace at
// /Users/Shared/yolo/yolo-jail broke `just format` with
//
//	fatal: Invalid path '/Users/Shared/yolo': Operation not permitted
//
// because `git ls-files` stats upward looking for the repository boundary.
//
// Why (literal) and not (subpath). A subpath grant on /Users/Shared/yolo would
// re-allow reads of every SIBLING checkout beside the workspace — precisely the
// isolation the /Users deny buys. (literal) grants the directory ENTRY alone, so
// traversal works and the siblings stay denied.
//
// /Users/Shared itself and /Users are already literal-allowed by the caller, and
// anything not under /Users/Shared/ contributes nothing: a path elsewhere under
// /Users (a real user's home) must NOT gain traversal grants from this, and the
// sandbox home at /Users/_yolojail is depth one so /Users already covers it.
func ancestorLiterals(paths ...string) string {
	const base = "/Users/Shared/"
	seen := map[string]bool{}
	var b strings.Builder
	for _, p := range paths {
		if !strings.HasPrefix(p, base) {
			continue
		}
		// Walk up from the parent, stopping before /Users/Shared.
		for dir := path.Dir(path.Clean(p)); strings.HasPrefix(dir, base); dir = path.Dir(dir) {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			b.WriteString("    (literal " + sbplStr(dir) + ")\n")
		}
	}
	return b.String()
}

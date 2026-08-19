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
func SeatbeltProfile(workspace, sandboxHome string) string {
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

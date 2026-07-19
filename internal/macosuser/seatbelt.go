package macosuser

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
		";;     home.  The workspace is NOT under any /Users/<name> home, so no\n" +
		";;     ancestor grant is needed. ---\n" +
		"(deny file-read* (subpath \"/Users\"))\n" +
		"(allow file-read*\n" +
		"    (literal \"/Users\")\n" +
		"    (literal \"/Users/Shared\")\n" +
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

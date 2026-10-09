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
// there. See docs/reference/host-execution-from-the-workspace.md §5.5, §5.6 item 1.
//
// Entries that are absolute or escape the workspace are dropped rather than
// emitted; config validation already rejects both (internal/config/validate.go
// validateWorkspaceReadonly), so this is defence in depth against a caller that
// skipped it, not a second error channel.
//
// # EVERY DENY CARRIES A `#seatbelt-test-id:<name>#`, AND THAT ID IS ITS PROOF
//
// Adopted from Agent Safehouse, which embeds the same marker in its `.sb` files so a
// rule and the case that proves it are greppable from each other
// (docs/research/agent-safehouse.md §8.1). The ids are SBPL comments, so they ship
// inside the profile a session actually installs — a human debugging a live sandbox
// can grep the file the kernel loaded, not just this generator.
//
// The other end is integration/macosuserseatbelt_test.go, which runs a command under
// the generated profile on a macOS runner and asserts the kernel refused it. Its
// registry holds every id in this file and, for each, either the runtime case that
// proves it or a written reason no case can. TestEverySeatbeltDenyCarriesATestID (here,
// on any OS) is the half that keeps a new deny from arriving without an id at all.
//
// # THE THREE DENIES TAKEN FROM SAFEHOUSE, 2026-09-18
//
// agent-safehouse.md §8.2, which is also the evidence: their 389-case suite runs real
// agents under these and they keep working. Deliberately NOT the deny-default
// inversion (§8.2 "why not the full inversion first", OQ-AS1) — this stays
// `(allow default)` with targeted denies.
//
//   - CROSS-PROCESS ARGV AND ENVIRONMENT. `(deny sysctl-read (sysctl-name-regex
//     #"procargs"))` plus `(deny process-info-pidinfo)`, with the same-sandbox
//     re-allow. Both channels, because Safehouse determined empirically on macOS 26
//     that EITHER ONE alone serves the read. This is the one directive here that takes
//     something away from `ps`: another process's command line stops being visible,
//     which is the point — a command line is where a secret sits in plain sight.
//   - `file-ioctl`, TERMINALS ONLY. `/dev/tty`, `/dev/ptmx` and the tty/pty device
//     names, which is what a TUI and every pty its tooling allocates need. ⚠ The
//     allow set is the part written blind: a pty an agent's tooling opens under some
//     OTHER name would lose its ioctls, and the runtime case that exercises this
//     (`script`, which allocates a real pty) is the instrument for it.
//   - `/System/Library/Keychains`, which this profile left readable while denying
//     `/Library/Keychains` beside it. ⚠ It holds SystemRootCertificates.keychain, the
//     system trust store. Trust evaluation on macOS goes through trustd over XPC
//     rather than by reading that file, so this should not reach TLS — but a tool that
//     reads the keychain file directly is the failure to watch for, and this deny is
//     one line to revert if one turns up.
//
// # THE SANDBOX HOME'S OWN CARVE-OUTS: THE STAGED SKILLS AND BRIEFINGS (G14)
//
// homeReadonly carries the content the bootstrap copied into the sandbox home — every
// staged skills dir and briefing — as the ABSOLUTE paths the kernel will see
// (ResolveHomeReadonly, homereadonly.go). It is rendered by homeReadonlyDenies, a
// SIBLING of readonlyDenies rather than more entries for it, because readonlyDenies drops
// absolute entries on purpose, and it is emitted in the same position for the same
// reason: after the writable-set allow that re-opens the sandbox home, and before
// anything that could re-open it again (nothing does). The zero value renders nothing, so
// a launch that delivered no content gets the profile it always got.
//
// # config.devices: A DECLARED DEVICE NODE GETS ITS CONTROL CALLS BACK (devices.go)
//
// The `file-ioctl` restriction above is what made a serial adapter unusable here: an `open`
// of /dev/cu.* already succeeds (reads are under `(allow default)` and /dev is in the
// writable set), and every tcsetattr on it is an ioctl. A raw-path `devices` entry the
// classifier admits (DeviceIoctlPaths) is re-allowed `file-ioctl` alone, LAST, after the deny
// it overrides (`#seatbelt-test-id:device-ioctl-allow#`). None declared renders nothing.
//
// # THE UNIFIED LOG IS UNREADABLE FROM THE SANDBOX, ALWAYS
//
// The profile denies reads of the log store and the lookup of the service a live stream
// connects to (macosLogDenies), and nothing later re-allows either. It used to be conditional
// on the top-level `macos_log` key, whose "user" and "full" settings rendered no rule so an
// in-sandbox `yolo-log` wrapper could run /usr/bin/log. That wrapper never read anything —
// the sandbox account cannot read the log even with no profile (macos-user CI run
// 37940733418) — so the key was retired, the log is read on the host by the `macos-log`
// loophole (packs/macos-log/README.md), and the deny no longer has a setting to yield to.
// The store paths and the service name are INFERRED; their proof is
// integration/macosuserseatbelt_test.go's.
//
// SeatbeltProfile is the profile of a launch with no context mount and no declared device.
func SeatbeltProfile(workspace, sandboxHome string, readonlyRels []string, homeReadonly HomeReadonly) string {
	return SeatbeltProfileWithContext(workspace, sandboxHome, readonlyRels, homeReadonly, nil, nil)
}

// profileWritableRoots is the writable set's fixed half: what the profile re-allows for
// file-write* besides the workspace and the sandbox home. ONE LIST, read by the profile below
// and by the context-mount siting (DarwinContextSiting's WritableRoots), so "a read-only source
// inside the writable set is refused" is never judged against a different set from the one the
// kernel is handed (docs/design/context-mounts.md §3.4).
var profileWritableRoots = []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders", "/dev"}

// bootVolume is the one entry under /Volumes the profile re-allows reads of.
const bootVolume = "/Volumes/Macintosh HD"

// SeatbeltProfileWithContext is SeatbeltProfile plus the CONTEXT MOUNTS this launch delivers by
// link (ctxlinks.go; docs/design/context-mounts.md §3.4). With none it is byte-identical to
// SeatbeltProfile. Each link adds its RESOLVED source to three blocks, each placed by what it
// must beat, because last-match-wins is decided among the rules that match ONE operation:
//
//   - a read-write source's write allow, right after the writable-set allow and before every
//     write deny that must win (`#seatbelt-test-id:context-write-allow#`);
//   - a read-only source's write deny, after every write allow
//     (`#seatbelt-test-id:context-readonly-deny#`);
//   - every source's read allow, after the /Users read deny it re-opens and before the keychain
//     denies, so no source can re-open a keychain (`#seatbelt-test-id:context-read-allow#`);
//
// and each source under /Users/Shared/ adds its intermediate directories to the ancestor
// literals: the traversal the workspace needed, with the siblings still denied.
//
// devices is config.devices' raw-path entries (deviceIoctlAllow).
//
// It renders no config TARGET outside the workspace: that is read off the workspace on disk,
// so BuildRunPlan, which reads it (workspaceReadonlyRels), calls seatbeltProfile itself.
func SeatbeltProfileWithContext(workspace, sandboxHome string, readonlyRels []string, homeReadonly HomeReadonly, ctx []ContextLink, devices []string) string {
	return seatbeltProfile(workspace, sandboxHome, readonlyRels, nil, homeReadonly, ctx, nil, devices)
}

// SeatbeltProfileWithRelocations is SeatbeltProfileWithContext plus the user's CACHE RELOCATIONS
// (ctxlinks.go's cache_relocations section): each resolved target gets a write allow beside the
// read-write context sources' and a read allow after the /Volumes and /Users read denies, which
// it re-opens, and before the keychain and unified-log denies, which still win
// (`#seatbelt-test-id:cache-relocation-write-allow#`, `#seatbelt-test-id:cache-relocation-read-allow#`).
// With none it is byte-identical to SeatbeltProfileWithContext.
func SeatbeltProfileWithRelocations(workspace, sandboxHome string, readonlyRels []string, homeReadonly HomeReadonly, ctx []ContextLink, relocs []CacheRelocation, devices []string) string {
	return seatbeltProfile(workspace, sandboxHome, readonlyRels, nil, homeReadonly, ctx, relocs, devices)
}

// seatbeltProfile is the one profile builder. readonlyTargets is the absolute half of
// workspace_readonly's lock, a symlinked config's target outside the workspace
// (workspaceReadonlyRels), rendered into the same deny form as readonlyRels (readonlyDenies).
func seatbeltProfile(workspace, sandboxHome string, readonlyRels, readonlyTargets []string, homeReadonly HomeReadonly, ctx []ContextLink, relocs []CacheRelocation, devices []string) string {
	if sandboxHome == "" {
		sandboxHome = SandboxHome()
	}
	ws := sbplStr(workspace)
	home := sbplStr(sandboxHome)
	ancestors := ancestorLiterals(append([]string{workspace, sandboxHome},
		contextSources(ctx, func(ContextLink) bool { return true })...)...)
	var writable strings.Builder
	for _, w := range profileWritableRoots {
		writable.WriteString("    (subpath " + sbplStr(w) + ")\n")
	}
	return "(version 1)\n" +
		";; yolo-jail macOS-user sandbox profile — SandVault-parity.\n" +
		";; Base allow with targeted denies; last match wins.\n" +
		"(allow default)\n" +
		"\n" +
		";; --- Writes: deny everywhere, then re-allow the agent's writable set ---\n" +
		";; #seatbelt-test-id:write-outside-deny#\n" +
		"(deny file-write* (subpath \"/\"))\n" +
		";; #seatbelt-test-id:workspace-write-allow#\n" +
		"(allow file-write*\n" +
		"    (subpath " + ws + ")\n" +
		"    (subpath " + home + ")\n" +
		strings.TrimSuffix(writable.String(), "\n") + ")\n" +
		contextWriteAllow(ctx) +
		relocationWriteAllow(relocs) +
		readonlyDenies(workspace, readonlyRels, readonlyTargets) +
		homeReadonlyDenies(homeReadonly) +
		contextReadonlyDeny(ctx) +
		"\n" +
		";; --- Volumes: deny reads except the boot volume ---\n" +
		";; #seatbelt-test-id:volumes-read-deny#\n" +
		"(deny file-read* (subpath \"/Volumes\"))\n" +
		";; #seatbelt-test-id:boot-volume-read-allow#\n" +
		"(allow file-read* (subpath " + sbplStr(bootVolume) + "))\n" +
		"\n" +
		";; --- Raw disk + packet capture: never ---\n" +
		";; #seatbelt-test-id:raw-device-deny#\n" +
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
		";; #seatbelt-test-id:users-read-deny#\n" +
		"(deny file-read* (subpath \"/Users\"))\n" +
		";; #seatbelt-test-id:workspace-read-allow#\n" +
		"(allow file-read*\n" +
		"    (literal \"/Users\")\n" +
		"    (literal \"/Users/Shared\")\n" +
		ancestors +
		"    (subpath " + ws + ")\n" +
		"    (subpath " + home + "))\n" +
		contextReadAllow(ctx) +
		relocationReadAllow(relocs) +
		"\n" +
		";; --- Keychains: System.keychain is world-readable (0644) on stock\n" +
		";;     macOS, so this deny is load-bearing ---\n" +
		";; #seatbelt-test-id:library-keychains-deny#\n" +
		"(deny file-read* (subpath \"/Library/Keychains\"))\n" +
		";; --- ...and the SYSTEM keychains beside them, which this profile left\n" +
		";;     readable while denying the pair above (agent-safehouse.md §8.2.3) ---\n" +
		";; #seatbelt-test-id:system-keychains-deny#\n" +
		"(deny file-read* (subpath \"/System/Library/Keychains\"))\n" +
		macosLogDenies() +
		"\n" +
		";; --- Process introspection the agent's tooling needs ---\n" +
		"(allow process-info*)\n" +
		"(allow sysctl-read)\n" +
		"\n" +
		";; --- ...EXCEPT another process's command line and environment, which is\n" +
		";;     where a secret sits in plain sight.  BOTH channels are denied\n" +
		";;     because EITHER ONE alone serves the read; the re-allow puts back\n" +
		";;     the same-sandbox case, which is the agent looking at itself and at\n" +
		";;     its own children.  MUST FOLLOW the two allows above — last match\n" +
		";;     wins, and `process-info*` includes `process-info-pidinfo`. ---\n" +
		";; #seatbelt-test-id:cross-process-procargs-deny#\n" +
		"(deny sysctl-read (sysctl-name-regex #\"procargs\"))\n" +
		";; #seatbelt-test-id:cross-process-pidinfo-deny#\n" +
		"(deny process-info-pidinfo)\n" +
		";; #seatbelt-test-id:same-sandbox-pidinfo-allow#\n" +
		"(allow process-info-pidinfo (target same-sandbox))\n" +
		"\n" +
		";; --- ioctl: terminals only.  A tty/pty is what an agent's own TUI and\n" +
		";;     every pty its tooling allocates need; an ioctl on anything else is\n" +
		";;     a device the agent has no business driving. ---\n" +
		";; #seatbelt-test-id:file-ioctl-deny#\n" +
		"(deny file-ioctl)\n" +
		";; #seatbelt-test-id:file-ioctl-tty-allow#\n" +
		"(allow file-ioctl\n" +
		"    (literal \"/dev/tty\")\n" +
		"    (literal \"/dev/ptmx\")\n" +
		"    (regex #\"^/dev/ttys[0-9]\")\n" +
		"    (regex #\"^/dev/pty[a-z0-9]\"))\n" +
		deviceIoctlAllow(devices)
}

// relocationTargets is the de-duplicated resolved targets of relocs, in order; one that is not
// absolute is dropped, since a rule on it would match nothing and read as access.
func relocationTargets(relocs []CacheRelocation) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range relocs {
		if seen[r.Target] || !strings.HasPrefix(r.Target, "/") {
			continue
		}
		seen[r.Target] = true
		out = append(out, r.Target)
	}
	return out
}

// relocationWriteAllow re-allows writes under every relocation target. Beside the read-write
// context sources' allow and BEFORE every write deny that must win: none of them can overlap a
// target (SiteCacheRelocations refuses the workspace, the sandbox home and every context source),
// so the position keeps the write policy one readable unit rather than deciding an outcome.
func relocationWriteAllow(relocs []CacheRelocation) string {
	targets := relocationTargets(relocs)
	if len(targets) == 0 {
		return ""
	}
	return "\n" +
		";; --- cache_relocations (docs/plans/cache-relocation.md): each relocated\n" +
		";;     ~/.cache/<subdir> is a link to its RESOLVED target, the sandbox's own\n" +
		";;     cache bytes, so the target is writable. ---\n" +
		";; #seatbelt-test-id:cache-relocation-write-allow#\n" +
		"(allow file-write*\n" + subpathClauses(targets) + ")\n"
}

// relocationReadAllow re-allows reads under every relocation target, and the directory entries
// on the way to one under /Users/Shared/ or /Volumes/ as literals, so a tool can stat the chain
// while the siblings stay denied (ancestorLiterals' reason). AFTER the /Volumes and /Users read
// denies, which it re-opens — last match wins — and before the keychain and unified-log denies,
// so no target can re-open either.
func relocationReadAllow(relocs []CacheRelocation) string {
	targets := relocationTargets(relocs)
	if len(targets) == 0 {
		return ""
	}
	return "\n" +
		";; --- cache_relocations: every relocation target, by its RESOLVED path, and the\n" +
		";;     directory entries on the way to it.  The link in ~/.cache is only a name:\n" +
		";;     Seatbelt judges the target, so this allow is what opens it.  After the\n" +
		";;     /Volumes and /Users read denies it re-opens. ---\n" +
		";; #seatbelt-test-id:cache-relocation-read-allow#\n" +
		"(allow file-read*\n" + ancestorLiterals(targets...) + volumeAncestorLiterals(targets) +
		subpathClauses(targets) + ")\n"
}

// volumeAncestorLiterals is ancestorLiterals for paths under /Volumes: one `(literal "…")` line
// for /Volumes itself and for every directory strictly between it and each path, which the
// /Volumes read deny would otherwise refuse a tool stat'ing up the chain. A literal grants the
// entry and its listing, not what is below it: the sandbox can read the names of the mounted
// volumes, as the `/Users` literal already lets it read the names of the accounts, and nothing on
// any of them but the target.
func volumeAncestorLiterals(targets []string) string {
	const base = "/Volumes/"
	seen := map[string]bool{}
	var b strings.Builder
	for _, t := range targets {
		if !strings.HasPrefix(t, base) {
			continue
		}
		var chain []string
		for dir := path.Dir(path.Clean(t)); strings.HasPrefix(dir+"/", base); dir = path.Dir(dir) {
			chain = append([]string{dir}, chain...)
		}
		for _, dir := range chain {
			if !seen[dir] {
				seen[dir] = true
				b.WriteString("    (literal " + sbplStr(dir) + ")\n")
			}
		}
	}
	return b.String()
}

// macosLogDenies renders the unified-log rules every profile carries.
//
//   - file-read* of the two stores `log show` reads: the persisted entries under
//     /private/var/db/diagnostics and the format strings under /private/var/db/uuidtext.
//     Physical paths, because the kernel resolves /var before the policy is consulted.
//   - mach-lookup of com.apple.diagnosticd, the service `log stream` connects to. A program
//     writes its own log through logd, which no rule here names; that denying diagnosticd costs
//     a logging program nothing is part of what is unmeasured.
//
// Placed after every file-read re-allow in the profile (the workspace's, the context mounts')
// so none can re-open the stores; nothing after it allows a mach-lookup. ⚠ INFERRED, never
// loaded on a Mac: the two store paths and the service name. Their runtime proof is the
// macos_log case in integration/macosuserseatbelt_test.go, with the bare run as control.
func macosLogDenies() string {
	return ";; --- The unified log is unreadable from the sandbox: yolo-log reads it on the\n" +
		";;     host (the macos-log loophole).  Its stores, then its live stream.\n" +
		";;     Nothing below re-allows a file read or a mach lookup. ---\n" +
		";; #seatbelt-test-id:macos-log-deny#\n" +
		"(deny file-read*\n" +
		"    (subpath \"/private/var/db/diagnostics\")\n" +
		"    (subpath \"/private/var/db/uuidtext\"))\n" +
		";; #seatbelt-test-id:macos-log-stream-deny#\n" +
		"(deny mach-lookup (global-name \"com.apple.diagnosticd\"))\n"
}

// readonlyDenies renders the config.workspace_readonly block: ONE
// `(deny file-write* …)` form carrying one `(subpath "<ws>/<rel>")` clause per
// entry, then one `(literal "<target>")` per config target outside the workspace
// (workspaceReadonlyRels), or "" when there are none, so a profile without the key
// is byte-identical to the one this backend emitted before the key was wired.
//
// The "one deny per entry" spelling this comment carried until 2026-08-23 was
// wrong, and it had already been copied into
// docs/research/macos-support-matrix.md before anyone read the body.
//
// Placed immediately after the writable-set allow rather than at the end of the
// profile: both positions are correct (nothing later re-allows file-write*), and
// keeping the whole write policy — deny all, allow the agent's set, re-deny the
// carve-outs — readable as one unit is worth more than the freedom to append.
func readonlyDenies(workspace string, rels, targets []string) string {
	if len(rels) == 0 && len(targets) == 0 {
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
	// A symlinked workspace config's target outside the workspace (workspaceReadonlyRels):
	// yolo's own derivation, never a user entry, so it is the one absolute path this form
	// carries. A `literal`, since the target is a file. A path that is not absolute is a deny
	// the kernel never matches, so it is dropped rather than rendered as protection.
	seen := map[string]bool{}
	for _, t := range targets {
		if !strings.HasPrefix(t, "/") || seen[path.Clean(t)] {
			continue
		}
		seen[path.Clean(t)] = true
		b.WriteString("    (literal " + sbplStr(path.Clean(t)) + ")\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n" +
		";; --- config.workspace_readonly: host-executed paths the agent must not\n" +
		";;     write.  Must follow the allow above — last match wins. ---\n" +
		";; #seatbelt-test-id:workspace-readonly-deny#\n" +
		"(deny file-write*\n" + strings.TrimSuffix(b.String(), "\n") + ")\n"
}

// homeReadonlyDenies renders the sandbox home's content carve-outs (G14): TWO forms, one
// per kind of entry, or "" when there are none — so a launch that delivered no skills and
// no briefing gets a profile byte-identical to the one it got before this existed.
//
//   - `(deny file-write* (subpath …))` over every delivered destination. file-write* is
//     every file-write-* operation — data, create, unlink, mode, owner, flags, times,
//     xattrs — so whichever of them the kernel checks a rename or a delete as, it is
//     denied: "the agent can neither modify, rename nor delete what it was given". The
//     shape was measured on hardware on 2026-09-16 (setup-support-gaps.md §5.1 row 14:
//     `touch` refused, `claude --version` and `claude mcp list` unaffected); the rename,
//     delete and plant cases are integration/macosuserseatbelt_test.go's, unrun on a Mac.
//   - `(deny file-write-create file-write-unlink (literal …))` over the anchors — the
//     chain ABOVE each destination. Deliberately NOT file-write*: an anchor is also the
//     agent's own state directory (`~/.claude`, `~/.pi/agent`), and refusing a chmod or a
//     utimes on it would be a new way to break an agent at startup that the container's
//     `:ro` bind never had. Unlink stops the chain being moved aside; create stops
//     anything being put where it was.
//
// Entries that are not absolute are dropped rather than emitted, the mirror image of
// readonlyDenies dropping absolute ones: a relative SBPL path is not a path the kernel ever
// reports, so it would be a deny that reads as protection and matches nothing.
func homeReadonlyDenies(h HomeReadonly) string {
	render := func(filter string, entries []string) string {
		var b strings.Builder
		seen := map[string]bool{}
		for _, p := range entries {
			if !strings.HasPrefix(p, "/") || seen[p] {
				continue
			}
			seen[p] = true
			b.WriteString("    (" + filter + " " + sbplStr(path.Clean(p)) + ")\n")
		}
		return strings.TrimSuffix(b.String(), "\n")
	}
	subpaths := render("subpath", h.Paths)
	literals := render("literal", h.Anchors)
	if subpaths == "" && literals == "" {
		return ""
	}
	out := "\n" +
		";; --- The sandbox home's staged skills and briefings: delivered by COPY, so\n" +
		";;     write-protected here the way every container backend's `:ro` bind\n" +
		";;     protects them.  Physical paths — the kernel resolves the home-tier\n" +
		";;     layout's symlinks before this is consulted.  Must follow the allow\n" +
		";;     above — last match wins. ---\n"
	if subpaths != "" {
		out += ";; #seatbelt-test-id:home-content-write-deny#\n" +
			"(deny file-write*\n" + subpaths + ")\n"
	}
	if literals != "" {
		out += ";; --- ...and every directory and layout link above them, so the chain\n" +
			";;     cannot be moved aside and replaced by one the agent wrote. ---\n" +
			";; #seatbelt-test-id:home-content-anchor-deny#\n" +
			"(deny file-write-create file-write-unlink\n" + literals + ")\n"
	}
	return out
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

package macosuser

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE DENY-BY-DEFAULT PROFILE (docs/design/macos-user-model.md §7, plan slice 4).
//
// DenyDefaultProfile renders the profile the ruled model runs every macos-user launch under:
// `(deny default)`, then an allow list, then a closing block of explicit denies. It is the
// prototype (docs/plans/runbooks/macos-user-model-prototype/profile.sb, measured on a Mac on
// 2026-10-22) ported block by block, in its order, with its `[source]` tags and its
// Apache-2.0 header, which ships inside every rendered profile.
//
// It sits BESIDE seatbeltProfile (seatbelt.go), which every account-mode launch still installs.
// The run-as-you mode (macos_user_mode "self", slice 6 of docs/design/macos-user-model-plan.md)
// installs this one (BuildRunPlanWithStages).
//
// Values are rendered as literal text, escaped with sbplStr, never as `sandbox-exec -D`
// parameters (MMP-D1): the prototype's eight ancestor slots and four port slots were a
// script's limit, not a design.
//
// SBPL IS LAST-MATCH-WINS. The order below carries three invariants the tests pin:
//
//   - `(deny default)` precedes every allow;
//   - every write allow precedes the write carve-outs (workspace_readonly, the staged home
//     content, read-only context sources), so nothing re-opens one;
//   - every allow precedes the closing block, which ends with `(deny appleevent-send)` as the
//     profile's last line, so a context source or relocation naming `~/.ssh` or a keychain
//     stays denied.
//
// The user the profile runs as is NOT a rule input: the rules are the same in both modes,
// which is the ruling ("Which user runs it is a parameter of the profile, not a second
// profile"). ProfileParams.RunAsUser is rendered into the header comment only, so a human
// reading an installed profile can tell which mode wrote it.

// TmpPolicy is the shared-/tmp ruling (OQ-MM8, ruled 2026-10-23: closed, with a hatch;
// MMP-D18). It governs /tmp alone, in both spellings. The running user's DARWIN_USER_TEMP_DIR
// is granted under either value: Apple's mktemp(3) callers, Java and Chrome write there
// whatever TMPDIR says.
type TmpPolicy int

const (
	// TmpShared grants /tmp read-write: the prototype's posture, the zero value, and what the
	// user-scope `macos_user_shared_tmp` hatch selects.
	TmpShared TmpPolicy = iota
	// TmpPrivate grants none of /tmp: the production value. The launch's own TMPDIR
	// (ProfileParams.SandboxTmp) and the session's host-services dir (ServicesDirs) keep their
	// own grants.
	TmpPrivate
)

// The production values of the two rulings (MMP-D13), set to the prototype's
// choices until slice 11 records the maintainer's answers. Each ruling is a one-line edit. The
// third, loopback (OQ-MM7), is ruled: the whole loopback is allowed, and is no parameter
// (networkBlocks, MMP-D14).
var (
	// productionTmp (OQ-MM8, ruled 2026-10-23): the shared /tmp is closed. A launch whose user
	// set `macos_user_shared_tmp` opens it (PlanSession.SharedTmp).
	productionTmp = TmpPrivate
	// productionHostSoftware (OQ-MM9): the install trees the prototype left readable.
	// /Applications is here rather than in the system list because OQ-MM9 names it with the
	// other two: the alternative it weighs is nix-only tools, which hides all three.
	productionHostSoftware = []string{"/opt/homebrew", "/usr/local", "/Applications"}
)

// hostSoftwareUnderSystemRoots are the host software trees a system read root already
// covers: /usr/local, Intel Homebrew, sits under /usr. When ProfileParams.HostSoftware does
// not name one, the profile denies it right after the system allow.
var hostSoftwareUnderSystemRoots = []string{"/usr/local"}

// ProfileParams is every input of DenyDefaultProfile. Paths must be PHYSICAL (the kernel
// resolves symlinks before the policy is consulted, so a rule on /tmp or an unresolved
// /var/folders path matches nothing). A path that is not absolute is dropped rather than
// rendered, since a rule on it would read as access and match nothing. Trailing slashes are
// removed (`getconf DARWIN_USER_TEMP_DIR` returns one).
type ProfileParams struct {
	// Workspace is read-write, at its real path.
	Workspace string
	// Home is the agent's own HOME (<workspace>/.yolo/home in the ruled model), read-write.
	Home string
	// HostHome is the invoking human's home. It is named only to deny parts of it, and to
	// read its locale preferences (CoreFoundation finds them through the account's home).
	HostHome string
	// HostHomeStat grants file-read-metadata on HostHome itself and each directory above it: a
	// stat, never a listing or a read below it. A launch's granted roots already reach it (its
	// machine root is under HostHome, so the ancestor literals name it); a run-as-you capture's
	// do not, and its bootstrap must stat the real home to run as you (paths.GuestRealHome,
	// MMP-D22).
	HostHomeStat bool
	// SandboxTmp is the launch's own TMPDIR, read-write under every TmpPolicy, and one of the
	// roots the sandbox may bind and connect unix sockets under.
	SandboxTmp string
	// DarwinUserTempDir is `getconf DARWIN_USER_TEMP_DIR` of the running user, read-write
	// only under TmpShared.
	DarwinUserTempDir string
	// StagingRoot is the host-owned staging tree (StagingRoot()), read only (MMP-D8).
	StagingRoot string
	// StagedReads, when set, narrow that read to the paths under StagingRoot this session's
	// sandbox reads — its guest bin, pack tree, context tree, capture store and its own env and
	// CA files — so it cannot read another session's env file or another workspace's context
	// copy. The directories above them get metadata alone, which lists nothing. Empty reads the
	// whole root.
	StagedReads []string
	// FontReads are the font folders the session's fonts.conf lists beyond the system read
	// roots, opened read-only (fontsconf.go's userFontsReadAllow): ~/Library/Fonts, and a
	// MobileAsset folder's real path outside /System/Library.
	FontReads []string
	// ServicesDirs are this session's own host-services dirs (internal/cli/run's
	// servicessession.go), read only, each by its exact path (MMP-D15): the endpoint files the
	// sandbox is told about live there, and the shared-/tmp allow must not be what reaches them,
	// since it reaches every other session's too. The dir's liveness lock is denied.
	ServicesDirs []string
	// MachineDirs are the writable roots outside the workspace and HOME: the machine root's
	// shared dirs (MMP-D6). Read-write.
	MachineDirs []string
	// Context are the context mounts, at their resolved original paths: read, plus write for
	// a read-write one.
	Context []ContextLink
	// Relocations are the user's cache relocations, read-write at their resolved targets.
	Relocations []CacheRelocation
	// CacheAliases are the host package caches shared in the run-as-you mode (cachealias.go,
	// MMP-D12), read-write at their resolved targets. Always empty in the account mode.
	CacheAliases []CacheAlias
	// ReadonlyRels and ReadonlyTargets are workspace_readonly's two halves (readonlyDenies).
	ReadonlyRels    []string
	ReadonlyTargets []string
	// HomeReadonly is the staged skills and briefings in HOME (homeReadonlyDenies).
	HomeReadonly HomeReadonly
	// Devices is config.devices' raw-path entries; each admitted node gets file access and
	// its control calls.
	Devices []string
	// RunAsUser names the user the profile runs as ("you" or "_yolojail"). Header comment
	// only: it changes no rule.
	RunAsUser string

	// Tmp and HostSoftware are the two pending rulings (MMP-D13). Tmp's zero value is the
	// prototype's posture; HostSoftware's nil hides every host software tree.
	// WithProductionRulings fills both with the production values.
	Tmp TmpPolicy
	// HostSoftware are the host install trees the sandbox may read (and so run from):
	// install trees such as /opt/homebrew, never a parent such as /opt (OQ-MM2).
	HostSoftware []string
}

// WithProductionRulings returns p with the two pending rulings set to their production values
// (productionTmp, productionHostSoftware).
func (p ProfileParams) WithProductionRulings() ProfileParams {
	p.Tmp = productionTmp
	p.HostSoftware = slices.Clone(productionHostSoftware)
	return p
}

// absClean returns the cleaned path, or "" when it is not absolute.
func absClean(p string) string {
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	return path.Clean(p)
}

// absCleanAll cleans every absolute path, drops the rest, and de-duplicates in order.
func absCleanAll(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if c := absClean(p); c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// within reports whether p is root or below it.
func within(p, root string) bool {
	return p == root || root == "/" || strings.HasPrefix(p, root+"/")
}

// ancestorLiteralsAll renders one `(literal "…")` line per directory strictly between / and
// each path, skipping a directory that is itself one of the paths or under one (that is
// granted whole already). The general form of ancestorLiterals, which covers only
// /Users/Shared/ and keeps that shape for the old profile. A literal grants one directory's
// entry and listing, never what is below it: a sibling of the workspace stays unreadable even
// though its NAME is listed.
func ancestorLiteralsAll(paths ...string) string {
	// "/" is no root to walk to: the caller grants its literal itself.
	clean := slices.DeleteFunc(absCleanAll(paths...), func(p string) bool { return p == "/" })
	seen := map[string]bool{}
	var b strings.Builder
	for _, p := range clean {
		var chain []string
		for dir := path.Dir(p); dir != "/"; dir = path.Dir(dir) {
			chain = append([]string{dir}, chain...)
		}
		for _, dir := range chain {
			if seen[dir] || slices.ContainsFunc(clean, func(c string) bool { return within(dir, c) }) {
				continue
			}
			seen[dir] = true
			b.WriteString("    (literal " + sbplStr(dir) + ")\n")
		}
	}
	return b.String()
}

// subpathLines renders one `(subpath "…")` line per path, each newline-terminated.
func subpathLines(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString("    (subpath " + sbplStr(p) + ")\n")
	}
	return b.String()
}

// form closes a block of clause lines into `(<head>\n<clauses>)\n`, or "" with no clauses.
func form(head, clauses string) string {
	if clauses == "" {
		return ""
	}
	return "(" + head + "\n" + strings.TrimSuffix(clauses, "\n") + ")\n"
}

// DenyDefaultProfile renders the deny-by-default profile for p. See the file header.
func DenyDefaultProfile(p ProfileParams) string {
	ws := absClean(p.Workspace)
	home := absClean(p.Home)
	hostHome := absClean(p.HostHome)
	sandboxTmp := absClean(p.SandboxTmp)
	userTmp := absClean(p.DarwinUserTempDir)
	staging := absClean(p.StagingRoot)
	machine := absCleanAll(p.MachineDirs...)
	hostSoftware := absCleanAll(p.HostSoftware...)

	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString(denyDefaultHeader(p.RunAsUser))

	b.WriteString("\n;; [00-base] The whole posture: nothing is allowed until a rule below says so.\n" +
		";; #seatbelt-test-id:deny-default#\n" +
		"(deny default)\n")

	b.WriteString(systemReadBlocks(hostSoftware, hostHome))

	// ---- The agent's own files.
	b.WriteString("\n;; ---------------------------------------------------------------------------\n" +
		";; The agent's own files: workspace, home and temp, the writable places.\n" +
		";; ---------------------------------------------------------------------------\n")

	ancestorInputs := append([]string{ws, home, sandboxTmp, userTmp, staging}, machine...)
	ancestorInputs = append(ancestorInputs, contextSources(p.Context, func(ContextLink) bool { return true })...)
	ancestorInputs = append(ancestorInputs, relocationTargets(p.Relocations)...)
	for _, a := range p.CacheAliases {
		ancestorInputs = append(ancestorInputs, a.Target)
	}
	b.WriteString("\n;; [render] Every directory above the workspace, home, temp dir and every other\n" +
		";; granted root, as literals: Claude Code and git readdir()/stat each ancestor at\n" +
		";; start. A literal grants that one directory's entry and listing, never what is\n" +
		";; below it, so a sibling of the workspace (or a file in your home, when the\n" +
		";; workspace is in it) stays unreadable even though its NAME is listed.\n" +
		";; #seatbelt-test-id:ancestor-literal-allow#\n" +
		form("allow file-read*", "    (literal \"/\")\n"+ancestorLiteralsAll(ancestorInputs...)))

	if ws != "" {
		b.WriteString("\n;; [yolo] The workspace, at its real path (no relocation on macOS).\n" +
			";; #seatbelt-test-id:workspace-read-allow#\n" +
			"(allow file-read* (subpath " + sbplStr(ws) + "))\n" +
			";; #seatbelt-test-id:workspace-write-allow#\n" +
			"(allow file-write* (subpath " + sbplStr(ws) + "))\n")
	}
	if home != "" {
		b.WriteString("\n;; [yolo] The agent's own HOME (<workspace>/.yolo/home in the ruled model).\n" +
			";; #seatbelt-test-id:home-allow#\n" +
			"(allow file-read* file-write* (subpath " + sbplStr(home) + "))\n")
	}
	if sandboxTmp != "" {
		b.WriteString("\n;; [yolo] The launch's own TMPDIR, under every /tmp policy.\n" +
			";; #seatbelt-test-id:tmp-allow#\n" +
			"(allow file-read* file-write* (subpath " + sbplStr(sandboxTmp) + "))\n")
	}
	if userTmp != "" {
		b.WriteString("\n;; [10-runtime] The running user's DARWIN_USER_TEMP_DIR, under every /tmp\n" +
			";; policy (OQ-MM8): Apple's mktemp callers, Java and Chrome write there regardless\n" +
			";; of TMPDIR (confstr). The user's .../C cache dir is NOT granted. Narrower than\n" +
			";; Safehouse, which grants all of /var/folders.\n" +
			";; #seatbelt-test-id:" + UserTmpAllowID + "#\n" +
			"(allow file-read* file-write* (subpath " + sbplStr(userTmp) + "))\n")
	}
	if p.Tmp == TmpShared {
		b.WriteString("\n;; [10-runtime] The shared /tmp, opened by the macos_user_shared_tmp hatch\n" +
			";; (OQ-MM8: closed by default). Every process of the running user shares it, so\n" +
			";; in self mode your own files there are readable. Both spellings.\n" +
			";; #seatbelt-test-id:" + SharedTmpAllowID + "#\n" +
			form("allow file-read* file-write*", subpathLines([]string{"/private/tmp", "/tmp"})))
	}
	if len(machine) > 0 {
		b.WriteString("\n;; [yolo] The machine root's shared dirs (MMP-D6), read-write.\n" +
			";; #seatbelt-test-id:machine-dir-allow#\n" +
			form("allow file-read* file-write*", subpathLines(machine)))
	}

	b.WriteString("\n;; [10-runtime] Device nodes for shell I/O, PTYs and descriptor plumbing.\n" +
		";; #seatbelt-test-id:dev-io-allow#\n" +
		"(allow file-read* file-write*\n" +
		"    (subpath \"/dev/fd\")                                               ;; Process substitution and /dev/fd/N.\n" +
		"    (literal \"/dev/stdout\")                                           ;; Standard output device.\n" +
		"    (literal \"/dev/stderr\")                                           ;; Standard error device.\n" +
		"    (literal \"/dev/null\")                                             ;; Null sink/source.\n" +
		"    (literal \"/dev/tty\")                                              ;; Controlling terminal.\n" +
		"    (literal \"/dev/ptmx\")                                             ;; PTY multiplexer for pseudo-terminal allocation.\n" +
		"    (literal \"/dev/dtracehelper\")                                     ;; dyld startup probe on some binaries.\n" +
		"    (regex #\"^/dev/ttys\")                                             ;; macOS pseudo-terminal slaves.\n" +
		"    (regex #\"^/dev/tty\")                                              ;; Dynamic tty names.\n" +
		"    (regex #\"^/dev/pty\"))                                             ;; Legacy PTY names.\n")
	b.WriteString(denyDefaultDeviceAllow(p.Devices))

	// The other write allows, then every write carve-out after all of them (last match wins).
	b.WriteString(contextWriteAllow(p.Context))
	b.WriteString(relocationWriteAllow(p.Relocations))
	// The shared package caches (MMP-D12), read-write: with the other write allows, so the
	// write carve-outs below stay terminal, and before the closing denies, which still beat it.
	b.WriteString(CacheAliasAllow(p.CacheAliases))
	if ws != "" {
		b.WriteString(readonlyDenies(ws, p.ReadonlyRels, p.ReadonlyTargets))
	}
	b.WriteString(homeReadonlyDenies(p.HomeReadonly))
	b.WriteString(contextReadonlyDeny(p.Context))

	b.WriteString("\n;; [10-runtime] Read-only entropy and probe devices.\n" +
		"(allow file-read*\n" +
		"    (literal \"/dev/zero\")                                             ;; Zero source.\n" +
		"    (literal \"/dev/autofs_nowait\")                                    ;; Automount readiness probe made by most CLIs.\n" +
		"    (literal \"/dev/urandom\")                                          ;; Non-blocking entropy for crypto.\n" +
		"    (literal \"/dev/random\"))                                          ;; Blocking entropy.\n" +
		"\n;; [10-runtime] ioctl only on terminals (raw mode, window size) and dtracehelper.\n" +
		";; #seatbelt-test-id:file-ioctl-tty-allow#\n" +
		"(allow file-ioctl\n" +
		"    (literal \"/dev/dtracehelper\")                                     ;; dyld startup ioctl probe.\n" +
		"    (literal \"/dev/tty\")                                              ;; Controlling-terminal ioctls.\n" +
		"    (literal \"/dev/ptmx\")                                             ;; grantpt/unlockpt/ptsname on the PTY master.\n" +
		"    (regex #\"^/dev/ttys\")                                             ;; tcsetattr/window size on slave ttys.\n" +
		"    (regex #\"^/dev/tty\")                                              ;; Dynamic tty names.\n" +
		"    (regex #\"^/dev/pty\"))                                             ;; Legacy PTY names.\n")

	if staging != "" {
		b.WriteString(stagingReadAllow(staging, p.StagedReads))
	}
	if p.HostHomeStat {
		b.WriteString(HostHomeStatAllow(hostHome))
	}
	b.WriteString(servicesDirReadAllow(p.ServicesDirs))
	b.WriteString(contextReadAllow(p.Context))
	b.WriteString(relocationReadAllow(p.Relocations))
	b.WriteString(userFontsReadAllow(p.FontReads))

	b.WriteString(processBlocks())
	b.WriteString(serviceBlocks())
	b.WriteString(posixSemAllow())
	b.WriteString(networkBlocks(absCleanAll(ws, home, sandboxTmp)))
	b.WriteString(nixDaemonBlocks())
	b.WriteString(closingDenies(hostHome))
	return b.String()
}

// HostHomeStatID is the test id of HostHomeStatAllow's block.
const HostHomeStatID = "host-home-stat-allow"

// HostHomeStatAllow is ProfileParams.HostHomeStat's block: metadata alone on the real home and
// each directory above it, so os.Stat of the home (paths.GuestRealHome, the bootstrap's identity
// check) and the symlink walk of the scope rule succeed, while nothing in it is listed or read.
// "" when the home is not absolute.
func HostHomeStatAllow(hostHome string) string {
	h := absClean(hostHome)
	if h == "" || h == "/" {
		return ""
	}
	var meta strings.Builder
	for dir := h; dir != "/"; dir = path.Dir(dir) {
		meta.WriteString("    (literal " + sbplStr(dir) + ")\n")
	}
	return "\n;; [yolo] Your real home, metadata only (MMP-D22): the bootstrap stats it to run as you\n" +
		";; and to judge workspace scope by it. A stat lists nothing and reads nothing below it.\n" +
		";; #seatbelt-test-id:" + HostHomeStatID + "#\n" +
		form("allow file-read-metadata", meta.String())
}

// stagingReadAllow is the profile's read on the host-owned staging root: the whole root, or,
// with reads, each of those paths under it and metadata on the directories between.
func stagingReadAllow(staging string, reads []string) string {
	head := "\n;; [yolo] The host-owned staging root (MMP-D8): the staged yolo, env files and\n" +
		";; per-launch trees. READ only: no write allow names it.\n" +
		";; #seatbelt-test-id:staging-root-read-allow#\n"
	var own []string
	for _, r := range absCleanAll(reads...) {
		if r != staging && within(r, staging) {
			own = append(own, r)
		}
	}
	if len(own) == 0 {
		return head + "(allow file-read* (subpath " + sbplStr(staging) + "))\n"
	}
	slices.Sort(own)
	var dirs []string
	for _, r := range own {
		for d := path.Dir(r); within(d, staging); d = path.Dir(d) {
			if !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
			if d == staging {
				break
			}
		}
	}
	slices.Sort(dirs)
	var b strings.Builder
	b.WriteString("\n;; [yolo] The host-owned staging root (MMP-D8), this session's own paths alone: another\n" +
		";; session's env file, or another workspace's context copy, beside them stays unreadable.\n" +
		";; The directories above them get metadata only. READ only: no write allow names it.\n" +
		";; #seatbelt-test-id:staging-root-read-allow#\n")
	b.WriteString("(allow file-read-metadata\n")
	for _, d := range dirs {
		b.WriteString("    (literal " + sbplStr(d) + ")\n")
	}
	b.WriteString(")\n(allow file-read*\n")
	for _, r := range own {
		b.WriteString("    (subpath " + sbplStr(r) + ")\n")
	}
	b.WriteString(")\n")
	return b.String()
}

// denyDefaultHeader is the profile's opening comment: what it is, the Safehouse attribution
// and license, and the source tags. The attribution ships in every rendered profile.
func denyDefaultHeader(runAs string) string {
	runAsLine := ""
	if runAs = strings.TrimSpace(strings.ReplaceAll(runAs, "\n", " ")); runAs != "" {
		runAsLine = ";; Run as: " + runAs + ". The user changes no rule below: one profile, both modes.\n"
	}
	return "\n" +
		";; ===========================================================================\n" +
		";; yolo-jail macos-user profile: deny by default, allow a list.\n" +
		";;\n" +
		";; Generated per session by yolo (internal/macosuser/seatbeltdeny.go) from the\n" +
		";; prototype measured on macOS 26.5 (docs/plans/runbooks/macos-user-model-prototype).\n" +
		runAsLine +
		";;\n" +
		";; ---------------------------------------------------------------------------\n" +
		";; ATTRIBUTION AND LICENSE\n" +
		";;\n" +
		";; Most rules below are adapted from Agent Safehouse's layered profiles,\n" +
		";; https://github.com/eugene1g/agent-safehouse (main branch, read 2026-10-22),\n" +
		";; Copyright the Agent Safehouse authors, licensed under the Apache License,\n" +
		";; Version 2.0. You may not use those portions except in compliance with the\n" +
		";; License. You may obtain a copy of the License at\n" +
		";;\n" +
		";;     http://www.apache.org/licenses/LICENSE-2.0\n" +
		";;\n" +
		";; Unless required by applicable law or agreed to in writing, software\n" +
		";; distributed under the License is distributed on an \"AS IS\" BASIS, WITHOUT\n" +
		";; WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the\n" +
		";; License for the specific language governing permissions and limitations\n" +
		";; under the License.\n" +
		";;\n" +
		";; Modified by yolo-jail. Each block names the Safehouse file it comes from:\n" +
		";;   [00-base]     profiles/00-base.sb\n" +
		";;   [10-runtime]  profiles/10-system-runtime.sb\n" +
		";;   [20-network]  profiles/20-network.sb\n" +
		";;   [30-apple]    profiles/30-toolchains/apple-toolchain-core.sb\n" +
		";;   [50-ssh]      profiles/50-integrations-core/ssh-agent-default-deny.sb\n" +
		";;   [50-container] profiles/50-integrations-core/container-runtime-default-deny.sb\n" +
		";;   [render]      bin/lib/policy/render.sh (the generated ancestor literals)\n" +
		";;   [yolo]        written for yolo-jail; not in Safehouse\n" +
		";; The changes, from docs/design/macos-user-model.md section 7: no per-language\n" +
		";; home grants (the agent has its own HOME); /opt narrowed to named install trees;\n" +
		";; read-write only on the workspace, the agent's HOME, temp dirs and named roots;\n" +
		";; launchservicesd dropped from the service list; loopback denied except listed\n" +
		";; ports; a closing block of explicit denies.\n" +
		";;\n" +
		";; SBPL is last-match-wins: an allow re-opens what an earlier deny closed, and\n" +
		";; the explicit denies at the end beat every allow above them.\n" +
		";; ===========================================================================\n"
}

// systemReadBlocks is the prototype's system and software read roots, with the host software
// trees (OQ-MM9) rendered from the parameter, and the human's locale preferences.
func systemReadBlocks(hostSoftware []string, hostHome string) string {
	var b strings.Builder
	b.WriteString("\n;; ---------------------------------------------------------------------------\n" +
		";; System and software read roots: the podman image's /usr, /bin, /nix/store.\n" +
		";; ---------------------------------------------------------------------------\n" +
		"\n;; [10-runtime], with /opt and /Applications moved to the host software block.\n" +
		";; #seatbelt-test-id:system-read-allow#\n" +
		"(allow file-read*\n" +
		"    (subpath \"/usr\")                                                  ;; Core system binaries and libraries; includes /usr/local (Intel Homebrew).\n" +
		"    (subpath \"/bin\")                                                  ;; POSIX userland binaries used by shells.\n" +
		"    (subpath \"/sbin\")                                                 ;; System utilities some build/test flows invoke.\n" +
		"    (subpath \"/nix/store\")                                            ;; Nix store: every nix-installed tool.\n" +
		"    (subpath \"/nix/var/nix/profiles\")                                 ;; Nix profile symlinks into the store.\n" +
		"    (subpath \"/System/Library\")                                       ;; macOS frameworks and resources; includes /System/Library/Fonts.\n" +
		"    (subpath \"/System/Volumes/Preboot\")                               ;; dyld/framework lookups resolve through Preboot (cryptexes) on modern macOS.\n" +
		"    (subpath \"/Library/Apple\")                                        ;; Apple private frameworks touched by native toolchains.\n" +
		"    (subpath \"/Library/Frameworks\")                                   ;; Global framework lookup path for language runtimes.\n" +
		"    (subpath \"/Library/Fonts\")                                        ;; System-wide fonts named by yolo's generated fonts.conf; ~/Library/Fonts: the font block.\n" +
		"    (subpath \"/Library/Filesystems/NetFSPlugins\")                     ;; Path resolution may load NetFS plugins.\n" +
		"    (subpath \"/Library/Preferences/Logging\")                          ;; Logging stack reads these prefs at process start.\n" +
		"    (literal \"/Library/Preferences/.GlobalPreferences.plist\")         ;; Locale/time defaults queried by runtimes.\n" +
		"    (literal \"/Library/Preferences/com.apple.networkd.plist\")         ;; DNS/network bootstrap reads networkd config.\n" +
		"    (literal \"/Library/Preferences/com.apple.dt.Xcode.plist\")         ;; Xcode's license record: refused it, the /usr/bin xcrun shims (make, cc, lldb) say the license is not accepted.\n" +
		"    (literal \"/dev\"))                                                 ;; /dev listing; shells enumerate it during start.\n")

	var hidden []string
	for _, t := range hostSoftwareUnderSystemRoots {
		if !slices.Contains(hostSoftware, t) {
			hidden = append(hidden, t)
		}
	}
	if len(hidden) > 0 {
		b.WriteString("\n;; [yolo] Host software trees a system root above covers, hidden (OQ-MM9: not\n" +
			";; visible). Right after the /usr allow, before anything that could re-open them.\n" +
			";; #seatbelt-test-id:host-software-hide-deny#\n")
		if len(hidden) == 1 {
			b.WriteString("(deny file-read* (subpath " + sbplStr(hidden[0]) + "))\n")
		} else {
			b.WriteString(form("deny file-read*", subpathLines(hidden)))
		}
	}
	if len(hostSoftware) > 0 {
		b.WriteString("\n;; [yolo] Host software install trees (OQ-MM9: visible), read-only: install trees\n" +
			";; only, never a parent such as /opt (OQ-MM2). Reading them is not launching an app.\n" +
			";; #seatbelt-test-id:host-software-read-allow#\n" +
			form("allow file-read*", subpathLines(hostSoftware)))
		// Metadata only on each tree's ancestors, so realpath() can lstat the parent of
		// /opt/homebrew without listing /opt (where /opt/src/clientX lives).
		var meta strings.Builder
		seen := map[string]bool{}
		for _, t := range hostSoftware {
			for dir := path.Dir(t); dir != "/"; dir = path.Dir(dir) {
				if !seen[dir] {
					seen[dir] = true
					meta.WriteString("    (literal " + sbplStr(dir) + ")\n")
				}
			}
		}
		if meta.Len() > 0 {
			b.WriteString(";; [yolo] Metadata only on their parents: a stat of /opt, never its listing.\n" +
				";; #seatbelt-test-id:host-software-metadata-allow#\n" +
				form("allow file-read-metadata", meta.String()))
		}
	}

	b.WriteString("\n;; [yolo] nix-darwin puts tools behind /run/current-system and /etc/profiles,\n" +
		";; symlinks into the store; resolving a symlink needs metadata on the link.\n" +
		"(allow file-read-metadata\n" +
		"    (literal \"/run\")                                                  ;; nix-darwin's synthetic /run symlink.\n" +
		"    (subpath \"/private/etc/profiles\"))                                ;; nix-darwin per-user profile links (targets are in /nix/store).\n" +
		"\n;; [10-runtime] Metadata-only traversal on system roots for loader/framework resolution.\n" +
		"(allow file-read-metadata\n" +
		"    (subpath \"/System\")                                               ;; stat/readdir traversal across /System during framework resolution.\n" +
		"    (subpath \"/private/var/run\")                                      ;; Metadata traversal of /private/var/run (socket namespace; connects are separate).\n" +
		"    (literal \"/pkg/env/global\"))                                      ;; macOS 27 PATH entry; a denied stat aborts Node's PATH search.\n" +
		"\n;; [10-runtime] /etc and /var are symlinks into /private; only the files runtimes need.\n" +
		"(allow file-read*\n" +
		"    (literal \"/private\")                                              ;; Symlink traversal under /private.\n" +
		"    (literal \"/private/var\")                                          ;; Symlink traversal to /var-backed paths.\n" +
		"    (subpath \"/private/var/db/timezone\")                              ;; Timezone database for date/time formatting.\n" +
		"    (literal \"/private/var/select/sh\")                                ;; /bin/sh selector.\n" +
		"    (literal \"/private/var/select/developer_dir\")                     ;; xcode-select developer dir pointer.\n" +
		"    (literal \"/var/select/developer_dir\")                             ;; Compatibility alias of the same.\n" +
		"    (literal \"/private/var/db/xcode_select_link\")                     ;; xcode-select link used by toolchain discovery.\n" +
		"    (literal \"/var/db/xcode_select_link\")                             ;; Compatibility alias of the same.\n" +
		"    (literal \"/private/etc/hosts\")                                    ;; Resolver host overrides.\n" +
		"    (literal \"/private/etc/resolv.conf\")                              ;; DNS resolver configuration.\n" +
		"    (literal \"/private/etc/services\")                                 ;; Service name-to-port mappings.\n" +
		"    (literal \"/private/etc/protocols\")                                ;; Protocol metadata for socket helpers.\n" +
		"    (literal \"/private/etc/shells\")                                   ;; Valid shell list.\n" +
		"    (subpath \"/private/etc/ssl\")                                      ;; TLS CA bundles used by curl, git and package clients.\n" +
		"    (literal \"/private/etc/localtime\")                                ;; Localtime symlink.\n" +
		"    (literal \"/etc\")                                                  ;; Compatibility symlink root.\n" +
		"    (literal \"/var\"))                                                 ;; Compatibility symlink root.\n" +
		"\n;; [10-runtime] Metadata-only probes some runtimes make during start.\n" +
		"(allow file-read-metadata\n" +
		"    (literal \"/home\")                                                 ;; Probed during path resolution by some runtimes.\n" +
		"    (literal \"/private/etc\")                                          ;; Directory traversal for /etc paths.\n" +
		"    (literal \"/nix\")                                                  ;; Directory traversal for /nix/store paths.\n" +
		"    (subpath \"/dev\"))                                                 ;; stat on /dev entries; readdir of /dev stats every node.\n")

	if hostHome != "" {
		b.WriteString("\n;; [10-runtime] The human's locale preferences, read-only. Safehouse grants\n" +
			";; these on HOME; here HOME is the agent's, but CoreFoundation finds\n" +
			";; preferences through the account's home, not $HOME. Locale only, no secrets.\n" +
			";; #seatbelt-test-id:host-preferences-read-allow#\n" +
			"(allow file-read*\n" +
			"    (prefix " + sbplStr(hostHome+"/Library/Preferences/.GlobalPreferences") + ")\n" +
			"    (prefix " + sbplStr(hostHome+"/Library/Preferences/com.apple.GlobalPreferences") + ")\n" +
			"    (literal " + sbplStr(hostHome+"/.CFUserTextEncoding") + "))\n")
	}

	b.WriteString("\n;; [30-apple] Apple Command Line Tools: the curated shim targets (git, make,\n" +
		";; clang and friends) and their support files. Debuggers stay out, as in Safehouse.\n" +
		";; #seatbelt-test-id:apple-toolchain-read-allow#\n" +
		"(allow file-read*\n")
	const clt = "/Library/Developer/CommandLineTools"
	for _, lit := range []string{"/Library", "/Library/Developer", clt, clt + "/usr", clt + "/usr/bin", clt + "/usr/libexec"} {
		b.WriteString("    (literal " + sbplStr(lit) + ")\n")
	}
	for _, tool := range []string{"git", "make", "gnumake", "c++", "cc", "clang", "clang++", "gcc", "g++",
		"cpp", "ar", "ranlib", "as", "ld", "libtool", "lipo", "strip", "nm", "otool",
		"codesign_allocate", "install_name_tool"} {
		b.WriteString("    (literal " + sbplStr(clt+"/usr/bin/"+tool) + ")\n")
	}
	for i, sub := range []string{"/usr/libexec/git-core", "/usr/include", "/usr/lib", "/usr/share", "/SDKs"} {
		end := "\n"
		if i == 4 {
			end = ")\n"
		}
		b.WriteString("    (subpath " + sbplStr(clt+sub) + ")" + end)
	}
	return b.String()
}

// denyDefaultDeviceAllow renders config.devices' admitted nodes. Under `(allow default)` an
// open of /dev/cu.* already succeeded and only the ioctl needed re-allowing
// (deviceIoctlAllow); under deny-default the open is refused too, so the node gets file
// access and its control calls in one form, under the same id.
func denyDefaultDeviceAllow(devices []string) string {
	allowed, _ := DeviceIoctlPaths(devices)
	if len(allowed) == 0 {
		return ""
	}
	var lits strings.Builder
	for _, d := range allowed {
		lits.WriteString("    (literal " + sbplStr(d) + ")\n")
	}
	return "\n;; [yolo] config.devices: each declared device node, opened and driven (a serial\n" +
		";; port's tcsetattr is an ioctl).\n" +
		";; #seatbelt-test-id:device-ioctl-allow#\n" +
		form("allow file-read* file-write* file-ioctl", lits.String())
}

// processBlocks is the prototype's process section: run anything, see and signal only the
// sandbox's own, and another process's argv and environment denied by both channels.
func processBlocks() string {
	return "\n;; ---------------------------------------------------------------------------\n" +
		";; Processes: run anything, see and signal only the sandbox's own.\n" +
		";; ---------------------------------------------------------------------------\n" +
		"\n;; [10-runtime] The podman PID namespace, as near as Seatbelt gets.\n" +
		";; #seatbelt-test-id:process-allow#\n" +
		"(allow process-exec)                                                  ;; Run toolchain binaries, shells and repo tools.\n" +
		"(allow process-fork)                                                  ;; Normal child-process trees.\n" +
		"(allow sysctl-read)                                                   ;; Runtime introspection at start (cpu count, os version).\n" +
		"(allow process-info* (target same-sandbox))                           ;; Inspect only processes inside this sandbox.\n" +
		"(allow signal (target same-sandbox))                                  ;; Signal only processes inside this sandbox; your processes are out of reach.\n" +
		"(allow mach-priv-task-port (target same-sandbox))                     ;; Same-sandbox task ports some runtimes need.\n" +
		"(allow pseudo-tty)                                                    ;; PTY allocation for interactive sessions.\n" +
		"\n;; [10-runtime] Another process's argv and environment: either channel alone\n" +
		";; serves the read (measured by Safehouse on macOS 26), so both are denied.\n" +
		";; Each deny follows the allow it narrows (last match wins).\n" +
		";; #seatbelt-test-id:cross-process-procargs-deny#\n" +
		"(deny sysctl-read (sysctl-name-regex #\"procargs\"))\n" +
		";; #seatbelt-test-id:cross-process-pidinfo-deny#\n" +
		"(deny process-info-pidinfo)\n" +
		";; #seatbelt-test-id:same-sandbox-pidinfo-allow#\n" +
		"(allow process-info-pidinfo (target same-sandbox))\n"
}

// serviceBlocks is the prototype's system-service allowlist. The keychain, pasteboard, Apple
// Events and LaunchServices are absent from it, and the closing block denies them by name.
func serviceBlocks() string {
	return "\n;; ---------------------------------------------------------------------------\n" +
		";; System services: an allowlist. Keychain (securityd), pasteboard, Apple\n" +
		";; Events and LaunchServices are absent from it, and the closing block denies\n" +
		";; them by name too (Apple Events by their own operation, appleevent-send).\n" +
		";; ---------------------------------------------------------------------------\n" +
		"\n;; [10-runtime] minus com.apple.coreservices.launchservicesd, which `open` uses.\n" +
		";; #seatbelt-test-id:mach-lookup-allow#\n" +
		"(allow mach-lookup\n" +
		"    (global-name \"com.apple.system.notification_center\")              ;; libnotify (notifyd) darwin notifications, not the user Notification Center.\n" +
		"    (global-name \"com.apple.system.opendirectoryd.libinfo\")           ;; User/group resolution (getpwuid).\n" +
		"    (global-name \"com.apple.logd\")                                    ;; Unified logging writes.\n" +
		"    (global-name \"com.apple.FSEvents\")                                ;; File event streams for watchers.\n" +
		"    (global-name \"com.apple.SystemConfiguration.configd\")             ;; Network configuration queries for resolver bootstrap.\n" +
		"    (global-name \"com.apple.SystemConfiguration.DNSConfiguration\")    ;; DNS configuration (Node/c-ares).\n" +
		"    (global-name \"com.apple.trustd.agent\")                            ;; Certificate trust evaluation: every HTTPS client (gh, mise, curl) asks it.\n" +
		"    (global-name \"com.apple.diagnosticd\")                             ;; Unified logging backend used by frameworks.\n" +
		"    (global-name \"com.apple.analyticsd\")                              ;; Analytics subsystem probed at framework start.\n" +
		"    (global-name \"com.apple.dnssd.service\")                           ;; DNS-SD used for name resolution by ssh, curl.\n" +
		"    (global-name \"com.apple.CoreServices.coreservicesd\")              ;; UTI/file type resolution. If `open -a` still works, this is the next to drop.\n" +
		"    (global-name \"com.apple.DiskArbitration.diskarbitrationd\")        ;; Disk metadata probed by some frameworks at start.\n" +
		"    (global-name \"com.apple.analyticsd.messagetracer\")                ;; Analytics message tracer probed at start.\n" +
		"    (global-name \"com.apple.system.logger\"))                          ;; Legacy syslog endpoint.\n" +
		"\n(allow system-socket)                                                 ;; [10-runtime] AF_SYSTEM sockets for kernel network event monitoring.\n" +
		"\n(allow ipc-posix-shm-read-data                                        ;; [10-runtime]\n" +
		"    (ipc-posix-name \"apple.shm.notification_center\"))                 ;; Shared memory read by libnotify.\n"
}

// PosixSemAllowID is the test id of the profile's POSIX named-semaphore allow (MMP-D37).
const PosixSemAllowID = "posix-sem-allow"

// posixSemAllow lets the sandbox use POSIX named semaphores, every name (MMP-D37). `(deny
// default)` refuses sem_open, and two things an agent's tools do all day are built on it:
//
//   - LMDB takes its reader and writer locks with a pair of named semaphores on macOS, `/MDBr…`
//     and `/MDBw…`. Pants keeps its local store in LMDB: each of the ten Pants engines checked,
//     2.22.0 to 2.33.2, carries both format strings and imports sem_open, sem_wait, sem_post and
//     sem_unlink, so refused them Pants cannot open its store;
//   - Python's multiprocessing makes one per Lock, and a Queue and a ProcessPoolExecutor are
//     built on Locks. Refused, each raises PermissionError.
//
// EVERY NAME, not a list of prefixes. A tool picks its own names, and a semaphore has no path:
// Seatbelt can scope one by name alone, so a list would only decide which tools fail, each with
// an error that names no semaphore. That is the loopback ruling's reasoning (MMP-D14). The bare
// operation is what Codex's base policy ships (codex-rs/sandboxing/src/seatbelt_base_policy.sbpl,
// read 2026-11-23); Safehouse's system-runtime profile has no semaphore rule.
//
// WHAT IT OPENS. A semaphore carries a count and no data. A named one made by a process outside
// the sandbox can be opened inside by name, then held, released or unlinked, where its mode
// admits the sandbox's user: in the run-as-you mode, every semaphore of yours. That is what lets
// the sandbox's Pants and yours take the same locks on the one store they share (cachealias.go),
// and it is also a way to stall a program of yours that locks with one, for whoever knows or
// guesses the name. Shared memory, which does carry data, is not widened: the profile's one shm
// rule stays libnotify's read.
//
// MEASURED on macOS 26.7.2 (2026-11-23) with sandbox-exec, under the profile this file renders:
// without the rule, sem_open(O_CREAT|O_EXCL) of `/MDBr…` and of `/mp-…` fails with EPERM, and
// Python 3.15's multiprocessing.Lock(), Queue() and ProcessPoolExecutor raise PermissionError;
// with it, LMDB's sequence (unlink, create, wait, post, open by name, unlink) passes. The bare
// operation covers the five sub-operations this macOS has: allowing ipc-posix-sem-create alone
// let the create through and refused the wait, the post and the unlink. And Pants 2.33.2 itself,
// in a throwaway project: without the rule `pants list ::` ends in "exceeded timeout of 60
// seconds while waiting for pantsd to start", each store shard holding a lock.mdb and no
// data.mdb; with it pantsd starts, its file watcher reports a new file to the next run, and a
// shell_command runs and is stored.
func posixSemAllow() string {
	// ASCII only, like the rest of the rendered profile.
	return "\n;; [yolo] POSIX named semaphores, every name (MMP-D37). LMDB takes its locks with a\n" +
		";; pair of them on macOS (/MDBr*, /MDBw*), so Pants cannot open its store without\n" +
		";; them, and Python's multiprocessing makes one per Lock, under every Queue and\n" +
		";; process pool. A tool picks its own names and Seatbelt can scope a semaphore by\n" +
		";; name alone, so no narrower rule keeps tools working. A semaphore carries a\n" +
		";; count, never data. What this opens: one a process of yours made can be opened\n" +
		";; here by name, where its mode admits the sandbox's user. Shared memory is not\n" +
		";; widened. The bare operation is Codex's\n" +
		";; (codex-rs/sandboxing/src/seatbelt_base_policy.sbpl).\n" +
		";; #seatbelt-test-id:" + PosixSemAllowID + "#\n" +
		"(allow ipc-posix-sem)                                                 ;; sem_open, sem_wait, sem_post, sem_unlink.\n"
}

// LoopbackAllowID is the test id of the profile's loopback allow (OQ-MM7, ruled 2026-10-23).
const LoopbackAllowID = "loopback-allow"

// UserTmpAllowID marks the running user's DARWIN_USER_TEMP_DIR grant (MMP-D18).
const UserTmpAllowID = "user-tmp-allow"

// SharedTmpAllowID marks the shared-/tmp grant, rendered only under TmpShared (MMP-D18).
const SharedTmpAllowID = "shared-tmp-allow"

// UnixSocketAllowID is the test id of the profile's unix-socket allow on the sandbox's own
// writable roots (MMP-D16).
const UnixSocketAllowID = "unix-socket-allow"

// networkBlocks is the prototype's network section, with the WHOLE LOOPBACK allowed (OQ-MM7,
// ruled 2026-10-23: the sandbox is an ordinary process on the Mac's own network stack, "network
// mode host", and Seatbelt cannot tell which process listens, so no narrower rule keeps the
// agent's own servers working; MMP-D14), and unix sockets allowed under socketRoots alone, the
// sandbox's own writable places (MMP-D16).
func networkBlocks(socketRoots []string) string {
	out := "\n;; ---------------------------------------------------------------------------\n" +
		";; Network: open, the host's loopback included (OQ-MM7); unix sockets only under\n" +
		";; the sandbox's own writable roots.\n" +
		";; ---------------------------------------------------------------------------\n" +
		"\n;; [20-network] Outbound TCP/UDP to anywhere: package installs, API calls; DNS\n" +
		";; through mDNSResponder's socket; listening, for dev servers the agent starts.\n" +
		";; #seatbelt-test-id:network-allow#\n" +
		"(allow network-outbound (remote ip))\n" +
		"(allow network-outbound (literal \"/private/var/run/mDNSResponder\"))\n" +
		"(allow network-bind (local ip))\n" +
		"(allow network-inbound (local ip))\n" +
		"\n;; [yolo] The whole loopback, every port (OQ-MM7: allowed). The sandbox shares the\n" +
		";; Mac's network stack, as a container on --net=host shares the host's: your\n" +
		";; loopback services are reachable, and so are servers the agent starts itself.\n" +
		";; Named although the allow above covers it, so no later edit can quietly drop it.\n" +
		";; #seatbelt-test-id:" + LoopbackAllowID + "#\n" +
		"(allow network-outbound (remote ip \"localhost:*\"))\n" +
		"(allow network-bind network-inbound (local ip \"localhost:*\"))\n"
	if len(socketRoots) == 0 {
		return out
	}
	var bind, connect strings.Builder
	for _, r := range socketRoots {
		bind.WriteString("    (local unix-socket (subpath " + sbplStr(r) + "))\n")
		connect.WriteString("    (remote unix-socket (subpath " + sbplStr(r) + "))\n")
	}
	return out + "\n;; [yolo] Unix sockets the sandbox creates in its own writable places: the workspace,\n" +
		";; its HOME and its TMPDIR (tmux, overmind, Python multiprocessing, ssh ControlMaster,\n" +
		";; a local database). bind() and connect() on a unix socket are network operations,\n" +
		";; not file ones, so the write allows above do not cover them. The subpath form is\n" +
		";; Codex's (codex-rs/sandboxing/src/seatbelt.rs). Never your launchd or ssh-agent\n" +
		";; sockets: they are outside these roots, and the closing block denies them by name.\n" +
		";; #seatbelt-test-id:" + UnixSocketAllowID + "#\n" +
		form("allow network-bind", bind.String()) +
		form("allow network-outbound", connect.String())
}

// ServicesDirReadAllowID is the test id of the read allow on this session's own host-services
// dirs (MMP-D15), and ServicesLockDenyID of the deny of the liveness lock inside each.
const (
	ServicesDirReadAllowID = "services-dir-read-allow"
	ServicesLockDenyID     = "services-lock-deny"
)

// servicesDirReadAllow is the read allow on this session's own host-services dirs, each by its
// exact path, with metadata only on each one's parent (a stat for path resolution, never a
// listing, which would name every other session's dir), and the dir's host-only liveness lock
// denied after it: a read-only descriptor can still take a flock, and a sandbox holding a shared
// lock would keep a dead session's dir from ever being collected.
func servicesDirReadAllow(dirs []string) string {
	clean := absCleanAll(dirs...)
	if len(clean) == 0 {
		return ""
	}
	var parents []string
	for _, d := range clean {
		if p := path.Dir(d); p != "/" && !slices.Contains(parents, p) {
			parents = append(parents, p)
		}
	}
	var meta, locks strings.Builder
	for _, p := range parents {
		meta.WriteString("    (literal " + sbplStr(p) + ")\n")
	}
	for _, d := range clean {
		for _, name := range []string{paths.HostServicesSessionLockName, paths.HostServicesSessionLockName + ".pending"} {
			locks.WriteString("    (literal " + sbplStr(d+"/"+name) + ")\n")
		}
	}
	return "\n;; [yolo] This session's own host-services dir (MMP-D15): the endpoint files the\n" +
		";; sandbox is told about, READ only, by exact path, so it reaches them whatever the\n" +
		";; /tmp ruling (OQ-MM8) decides. Another session's dir beside it gets nothing here.\n" +
		";; #seatbelt-test-id:" + ServicesDirReadAllowID + "#\n" +
		form("allow file-read-metadata", meta.String()) +
		form("allow file-read*", subpathLines(clean)) +
		";; [yolo] ...but never the session's liveness lock, which only the host holds.\n" +
		";; #seatbelt-test-id:" + ServicesLockDenyID + "#\n" +
		form("deny file-read* file-write*", locks.String())
}

// NixDaemonSocket is the host nix daemon's socket, which every nix client in the sandbox
// connects to (hostnix.go sets NIX_REMOTE=daemon): the store is the host's, and only the daemon
// writes it.
const NixDaemonSocket = "/nix/var/nix/daemon-socket/socket"

// nixDaemonBlocks lets the sandbox's nix client reach the host daemon: a connect to its socket,
// which the deny-default network section otherwise refuses (only mDNSResponder's socket is
// allowed there), and the reads the client makes before it connects — the daemon socket's
// directory and the system nix.conf, which nix-darwin links through /etc/static into the store.
// The store itself and the profiles are system read roots already. macos-user's acceptance bar
// is carrying the nix layer, so a profile that refused this would ship a sandbox whose `nix`
// cannot build anything.
func nixDaemonBlocks() string {
	return "\n;; [yolo] The host nix daemon: its socket (a connect is network-outbound, not a\n" +
		";; file read), the socket's directory, and the system nix.conf (nix-darwin links it\n" +
		";; through /etc/static into the store). The client builds nothing itself; the\n" +
		";; daemon, outside the sandbox, writes the store.\n" +
		";; #seatbelt-test-id:nix-daemon-allow#\n" +
		"(allow network-outbound (remote unix-socket (path-literal " + sbplStr(NixDaemonSocket) + ")))\n" +
		"(allow file-read*\n" +
		"    (subpath \"/nix/var/nix/daemon-socket\")                          ;; The socket and its directory.\n" +
		"    (subpath \"/private/etc/nix\")                                    ;; nix.conf and its includes.\n" +
		"    (literal \"/private/etc/static\"))                                ;; nix-darwin's link into the store.\n" +
		"(allow file-read-metadata\n" +
		"    (literal \"/nix/var\")\n" +
		"    (literal \"/nix/var/nix\"))                                       ;; Traversal to the socket.\n"
}

// regexPathStr renders a path-regex string matching rest under the literal prefix p, as an
// SBPL string (backslashes doubled by sbplStr).
func regexPathStr(p, rest string) string {
	return sbplStr("^" + regexp.QuoteMeta(p) + rest)
}

// closingDenies is the prototype's explicit-deny block, with the old profile's kept denies
// (keychains split into their two ids, raw devices, the unified log) added to it. Every form
// is a deny but the last line, which is the deny of Apple Events by their own operation.
func closingDenies(hostHome string) string {
	var b strings.Builder
	b.WriteString("\n;; ---------------------------------------------------------------------------\n" +
		";; Explicit denies. (deny default) already covers each; naming them here, last,\n" +
		";; means no allow above can re-open them by being written too wide. Nothing\n" +
		";; below this line allows anything.\n" +
		";; ---------------------------------------------------------------------------\n")
	if hostHome != "" {
		b.WriteString("\n;; [yolo] The human's secrets in their home.\n" +
			";; #seatbelt-test-id:host-secrets-deny#\n" +
			"(deny file-read* file-write*\n" +
			"    (subpath " + sbplStr(hostHome+"/.ssh") + ")    ;; ssh keys and config.\n" +
			"    (subpath " + sbplStr(hostHome+"/Library/Keychains") + ")    ;; Login keychain files.\n" +
			"    (subpath " + sbplStr(hostHome+"/Library/Application Support/com.apple.TCC") + "))    ;; Privacy grant database.\n")
	}
	b.WriteString("\n;; [yolo] System keychain files: System.keychain is world-readable on stock macOS.\n" +
		";; #seatbelt-test-id:library-keychains-deny#\n" +
		"(deny file-read* file-write* (subpath \"/Library/Keychains\"))\n" +
		";; [yolo] System root keychains, re-closed after the /System/Library allow; trust\n" +
		";; evaluation goes through trustd, not these files.\n" +
		";; #seatbelt-test-id:system-keychains-deny#\n" +
		"(deny file-read* file-write* (subpath \"/System/Library/Keychains\"))\n" +
		"\n;; [yolo] Raw disk and packet capture: never, whatever config.devices says.\n" +
		";; file-read-metadata is named because the /dev metadata allow above beats file-read*\n" +
		";; alone: measured on macOS 26.5, `ls -l /dev/disk0` ran under the deny without it.\n" +
		";; #seatbelt-test-id:raw-device-deny#\n" +
		"(deny file-read* file-write* file-read-metadata\n" +
		"    (regex #\"^/dev/r?disk\")\n" +
		"    (regex #\"^/private/dev/r?disk\")\n" +
		"    (regex #\"^/dev/bpf\"))\n" +
		"\n" + macosLogDenies())

	b.WriteString("\n;; [10-runtime] launchd's per-user listener sockets (ssh agent, pasteboard and others).\n" +
		";; #seatbelt-test-id:launchd-listener-deny#\n" +
		"(deny file-read* file-write* file-read-metadata\n" +
		"    (regex #\"^/private/tmp/com\\.apple\\.launchd\\.[^/]+/Listeners$\")     ;; Listener sockets, macOS 26.3 and earlier.\n" +
		"    (regex #\"^/tmp/com\\.apple\\.launchd\\.[^/]+/Listeners$\")             ;; The /tmp spelling of the same.\n" +
		"    (regex #\"^/private/var/run/com\\.apple\\.launchd\\.[^/]+/Listeners$\") ;; Listener sockets, macOS 26.4 and later.\n" +
		"    (regex #\"^/var/run/com\\.apple\\.launchd\\.[^/]+/Listeners$\"))        ;; The /var/run spelling of the same.\n")

	sshAgent := ""
	if hostHome != "" {
		sshAgent = "\n    (remote unix-socket (path-regex " + regexPathStr(hostHome, `/\.ssh/agent(/.*)?$`) + "))"
	}
	b.WriteString("\n;; [50-ssh] The ssh agent, by its network spelling: connect() on a unix socket\n" +
		";; is network-outbound, not a file read.\n" +
		";; #seatbelt-test-id:ssh-agent-socket-deny#\n" +
		"(deny network-outbound\n" +
		"    (remote unix-socket (path-regex #\"^/private/tmp/com\\.apple\\.launchd\\.[^/]+/Listeners$\"))\n" +
		"    (remote unix-socket (path-regex #\"^/tmp/com\\.apple\\.launchd\\.[^/]+/Listeners$\"))\n" +
		"    (remote unix-socket (path-regex #\"^/private/var/run/com\\.apple\\.launchd\\.[^/]+/Listeners$\"))\n" +
		"    (remote unix-socket (path-regex #\"^/var/run/com\\.apple\\.launchd\\.[^/]+/Listeners$\"))" +
		sshAgent + ")\n")

	fileSockets := "    (literal \"/var/run/docker.sock\")\n" +
		"    (literal \"/private/var/run/docker.sock\")\n" +
		"    (literal \"/var/run/podman/podman.sock\")\n" +
		"    (literal \"/private/var/run/podman/podman.sock\")\n"
	netSockets := "    (remote unix-socket (path-literal \"/var/run/docker.sock\"))\n" +
		"    (remote unix-socket (path-literal \"/private/var/run/docker.sock\"))\n" +
		"    (remote unix-socket (path-literal \"/var/run/podman/podman.sock\"))\n" +
		"    (remote unix-socket (path-literal \"/private/var/run/podman/podman.sock\"))\n"
	if hostHome != "" {
		for _, rel := range []string{"/.docker/run/docker.sock", "/.rd/docker.sock", "/.orbstack/run/docker.sock"} {
			fileSockets += "    (literal " + sbplStr(hostHome+rel) + ")\n"
			netSockets += "    (remote unix-socket (path-regex " + regexPathStr(hostHome+rel, "$") + "))\n"
		}
		netSockets += "    (remote unix-socket (path-regex " +
			regexPathStr(hostHome+"/.local/share/containers/podman/machine/", `.*\.sock$`) + "))\n"
	}
	b.WriteString("\n;; [50-container] Container daemon sockets: a container runtime is a way out.\n" +
		";; #seatbelt-test-id:container-socket-deny#\n" +
		form("deny file-read* file-write*", fileSockets) +
		";; #seatbelt-test-id:container-socket-connect-deny#\n" +
		form("deny network-outbound", netSockets))

	b.WriteString("\n;; [yolo] The services design section 5 names, by name, after the allowlist.\n" +
		";; #seatbelt-test-id:service-deny#\n" +
		"(deny mach-lookup\n" +
		"    (global-name \"com.apple.SecurityServer\")                          ;; securityd: the login keychain.\n" +
		"    (global-name \"com.apple.securityd\")                               ;; securityd under its other registered name.\n" +
		"    (global-name \"com.apple.securityd.xpc\")                           ;; securityd's XPC endpoint.\n" +
		"    (global-name \"com.apple.secd\")                                    ;; Data-protection keychain (iCloud Keychain items).\n" +
		"    (global-name \"com.apple.pasteboard.1\")                            ;; The clipboard.\n" +
		"    (global-name \"com.apple.coreservices.appleevents\")                ;; Apple Events: driving your other apps (osascript).\n" +
		"    (global-name \"com.apple.coreservices.launchservicesd\"))           ;; LaunchServices: `open -a`, opening files in your apps.\n")

	b.WriteString("\n;; [yolo] Apple Events by their own operation, last so nothing re-allows it.\n" +
		";; Defense in depth: on macOS 26.5 a real event to Finder already fails without it.\n" +
		";; #seatbelt-test-id:appleevent-send-deny#\n" +
		"(deny appleevent-send)\n")
	return b.String()
}

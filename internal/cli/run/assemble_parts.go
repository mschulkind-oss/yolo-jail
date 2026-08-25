package run

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// appleContainerBaseMounts builds the Apple Container base mounts: single
// writable /home/agent (device-limit workaround), the mise named volume, and
// bare --tmpfs scratch dirs.
//
// cache_relocations are skipped here (one warning for the whole set, not one per
// entry). Not because the backend cannot nest a bind mount — this very function
// mounts GlobalCache() at /home/agent/.cache inside the wsState → /home/agent
// mount, which is the same nesting depth a relocation needs — but because it is
// a separate mount path built around the single-writable-/home/agent device
// limit and nobody has verified relocation on real Apple Container hardware.
// Skipping loudly beats half-applying: a relocation that silently did not take
// leaves the jail writing the very bytes the user moved back onto the filesystem
// they moved them off.
//
// PACK-DECLARED HOME DIRS COME IN TWO TIERS AND THE SINGLE BIND ANSWERS ONLY ONE.
// Getting this wrong shipped a real bug (#39), so both are stated here:
//
//   - WritableDirs (per-workspace) needs no handling. Apple Container mounts the
//     whole wsState at /home/agent read-write in one bind, so every declared home
//     path is already writable and its writes already land in wsState — the same
//     per-workspace tier podman gives it with an explicit -v. A silent SUCCESS.
//   - SharedDirs (machine-wide) DOES need handling, and is mounted below. These
//     come from GlobalHome precisely so a credential outlives the workspace, and
//     the single bind puts them in wsState instead. Left to the bind it is a
//     silent DEGRADATION: ~/.claude-shared-credentials keeps working, so nothing
//     errors, but it is per-workspace forever and every new workspace demands a
//     fresh /login.
//
// The tell that separates them: ask which SIDE of the mount the podman argv reads
// from. wsState → the bind already covers it. paths.GlobalHome() → it does not,
// because that is a different directory on the host and no bind here reaches it.
func appleContainerBaseMounts(rt string, runFlags []string, workspace string, in *assembleInput, out printer) []string {
	wsState := in.wsState
	if len(in.cacheRelocations) > 0 {
		out.print("[yellow]Skipping cache_relocations (" + cacheRelocationSubdirs(in.cacheRelocations) +
			"): cache_relocations are not implemented on Apple Container, " +
			"so the cache stays on its original filesystem. " +
			"Use `YOLO_RUNTIME=podman` for cache relocation.[/yellow]")
	}
	runCmd := append([]string{rt, "run"}, runFlags...)
	runCmd = append(runCmd,
		"-v", workspace+":/workspace",
		"-v", wsState+":/home/agent",
		"-v", paths.GlobalCache()+":/home/agent/.cache",
		"-v", miseStoreVolume+":/mise",
		"--tmpfs", "/tmp",
		"--tmpfs", "/var/tmp",
		"--tmpfs", "/var/lib/containers",
		"--tmpfs", "/var/cache/containers",
		"--tmpfs", "/run",
		"--tmpfs", "/dev/shm",
	)
	// The machine-wide tier, nested inside the /home/agent bind exactly as
	// GlobalCache is above. This costs ONE mount per declared shared dir — two
	// today across every shipped pack (claude's and agy's) — not one per file in
	// them, so the mount-count pressure that forced the single-writable-home shape
	// in the first place is not meaningfully changed by it. (The issue reporting
	// this named a specific limit of 22; that number appears nowhere in this repo,
	// so it is deliberately not repeated here. What the repo knows is that there IS
	// a limit and that the single-home bind exists to respect it.)
	//
	// storage.Ensure MkdirAlls every EmbeddedSharedDirs() under GlobalHome on
	// every backend (internal/storage/ensure.go:54), so the host side exists
	// before this argv runs.
	//
	// NO MOUNTPOINT IS PRE-CREATED under wsState, and that is checked rather than
	// assumed: nothing creates <wsState>/.cache either, yet the GlobalCache mount
	// above lands at /home/agent/.cache on this backend today. The mountpoint is
	// auto-created inside the READ-WRITE parent bind. (podman needs the mountpoint
	// pre-created only because ITS /home/agent base is `:ro`, where crun's mkdirat
	// fails EROFS — see podmanBaseMounts. The two backends differ here for a reason
	// that is about the parent mount's mode, not about the nested dir.)
	for _, dir := range packload.SharedDirs(in.packs) {
		runCmd = append(runCmd, "-v",
			filepath.Join(paths.GlobalHome(), dir)+":/home/agent/"+dir)
	}
	return runCmd
}

// podmanBaseMounts builds the podman base mounts: the :ro GLOBAL_HOME base +
// the per-workspace writable overlays (dirs, files) + the mise store mount
// (named volume on macOS, bind dir otherwise).
// isMacOS comes from the Options seam, never paths.IsMacOS, so the golden argv
// is the same on every host.
func podmanBaseMounts(rt string, runFlags []string, workspace string, in *assembleInput, isMacOS bool) []string {
	ws := in.wsState
	runCmd := append([]string{rt, "run"}, runFlags...)
	runCmd = append(runCmd,
		"-v", workspace+":/workspace",
		"-v", paths.GlobalHome()+":/home/agent:ro",
		"-v", filepath.Join(ws, "npm-global")+":/home/agent/.npm-global",
		"-v", filepath.Join(ws, "local")+":/home/agent/.local",
		"-v", filepath.Join(ws, "go")+":/home/agent/go",
		"-v", filepath.Join(ws, "yolo-shims")+":/home/agent/.yolo-shims",
		// The launcher dir is a SECOND generated-script anchor, mounted for exactly the
		// same reason as yolo-shims: the entrypoint writes into it every boot and
		// /home/agent is :ro, so without its own rw bind the boot fails EROFS. It exists
		// separately because blockers must precede the real tool on PATH and lazy
		// installers must NOT (see entrypoint.Env.LauncherDir).
		"-v", filepath.Join(ws, "yolo-launchers")+":/home/agent/.yolo-launchers",
		"-v", filepath.Join(ws, "config")+":/home/agent/.config",
		"-v", paths.GlobalCache()+":/home/agent/.cache",
	)
	// Cache relocations: a rw bind nested INSIDE the .cache mount above, so
	// ~/.cache/<subdir> in the jail is an ordinary writable dir backed by other
	// storage. Emitted here purely for readability — podman sorts mounts by
	// destination depth, so being adjacent to (or after) the parent .cache mount
	// is not what makes it work; reversing the two args behaves identically.
	// Sorted so the argv is deterministic whatever order the caller collected
	// them in (config.LoadCacheRelocations already sorts; this keeps the argv's
	// guarantee local to the emitter).
	for _, rel := range sortedCacheRelocations(in.cacheRelocations) {
		runCmd = append(runCmd, "-v", rel.Target+":/home/agent/.cache/"+rel.Subdir)
	}
	runCmd = append(runCmd,
		"-v", filepath.Join(ws, "yolo-bootstrap.sh")+":/home/agent/.yolo-bootstrap.sh",
		"-v", filepath.Join(ws, "yolo-venv-precreate.sh")+":/home/agent/.yolo-venv-precreate.sh",
		"-v", filepath.Join(ws, "yolo-perf.log")+":/home/agent/.yolo-perf.log",
		"-v", filepath.Join(ws, "yolo-socat.log")+":/home/agent/.yolo-socat.log",
		"-v", filepath.Join(ws, "yolo-entrypoint.lock")+":/home/agent/.yolo-entrypoint.lock",
		"-v", filepath.Join(ws, "yolo-ca-bundle.crt")+":/home/agent/.yolo-ca-bundle.crt",
		"-v", filepath.Join(ws, "yolo-installed-lsps")+":/home/agent/.yolo-installed-lsps",
		"-v", filepath.Join(ws, "bash_history")+":/home/agent/.bash_history",
		"-v", filepath.Join(ws, "ssh")+":/home/agent/.ssh",
	)
	// Writable home dirs: extra $HOME subpaths (config writable_home_dirs) made
	// read-write by nesting a bind INSIDE the :ro GLOBAL_HOME base. The OCI
	// runtime does NOT auto-create mountpoints inside a :ro bind mount (crun
	// mkdirat fails with EROFS) — existing mounts (.npm-global etc.) work only
	// because those dirs already exist in GLOBAL_HOME. prepareWsState creates
	// the mountpoint in GLOBAL_HOME for each declared entry. Sorted for a
	// deterministic argv (the deriver already sorts; this keeps the guarantee
	// local to the emitter, matching the cache-relocation block above).
	for _, rel := range sortedWritableHomeDirs(in.writableHomeDirs) {
		runCmd = append(runCmd, "-v",
			filepath.Join(ws, config.WritableHomeBackingSubdir, rel)+":/home/agent/"+rel)
	}
	// mise store: named volume on macOS, bind dir otherwise.
	if isMacOS {
		runCmd = append(runCmd, "-v", miseStoreVolume+":/mise")
	} else {
		runCmd = append(runCmd, "-v", in.miseStore+":/mise")
	}
	return runCmd
}

// sortedWritableHomeDirs returns the paths sorted, without mutating the
// caller's slice (assembleRunCmd is a pure function of its input).
func sortedWritableHomeDirs(dirs []string) []string {
	out := append([]string(nil), dirs...)
	sort.Strings(out)
	return out
}

// sortedCacheRelocations returns the relocations ordered by subdir, without
// mutating the caller's slice (assembleRunCmd is a pure function of its input).
func sortedCacheRelocations(rels []config.CacheRelocation) []config.CacheRelocation {
	out := append([]config.CacheRelocation(nil), rels...)
	sort.Slice(out, func(i, j int) bool { return out[i].Subdir < out[j].Subdir })
	return out
}

// cacheRelocationSubdirs renders the relocated subdir names for a warning line.
func cacheRelocationSubdirs(rels []config.CacheRelocation) string {
	names := make([]string, 0, len(rels))
	for _, rel := range sortedCacheRelocations(rels) {
		names = append(names, rel.Subdir)
	}
	return strings.Join(names, ", ")
}

// podmanNestingArgs builds the podman nesting/GPU/device+cap block. One of three
// branches: in-container (share parent userns),
// GPU-nvidia (runc + identity uidmap), or the normal host branch (fuse + uidmap
// + caps).
func (o *Options) podmanNestingArgs(inContainer, gpuEnabled bool, gpuVendor string) []string {
	if inContainer {
		args := []string{
			"--security-opt", "label=disable",
			"--userns", "host",
			"--cap-add", "SYS_ADMIN",
			"--cap-add", "MKNOD",
			"--cap-add", "NET_ADMIN",
			"--cap-add", "NET_RAW",
		}
		for _, dev := range []string{"/dev/fuse", "/dev/net/tun"} {
			if o.PathExists(dev) {
				args = append(args, "--device", dev)
			}
		}
		return args
	}
	if gpuEnabled && gpuVendor == "nvidia" {
		return []string{
			"--security-opt", "label=disable",
			"--uidmap", "0:0:1",
			"--uidmap", "1:1:65536",
			"--gidmap", "0:0:1",
			"--gidmap", "1:1:65536",
			"--runtime", "runc",
			"--cap-add", "SYS_ADMIN",
			"--cap-add", "NET_ADMIN",
			"--cap-add", "NET_RAW",
		}
	}
	args := []string{
		"--security-opt", "label=disable",
		"--device", "/dev/fuse",
		"--uidmap", "0:0:1",
		"--uidmap", "1:1:65536",
		"--gidmap", "0:0:1",
		"--gidmap", "1:1:65536",
		"--cap-add", "SYS_ADMIN",
		"--cap-add", "MKNOD",
		"--cap-add", "NET_ADMIN",
		"--cap-add", "NET_RAW",
	}
	if o.PathExists("/dev/net/tun") {
		args = append(args, "--device", "/dev/net/tun")
	}
	return args
}

// gitIdentityMountArgs composes the jail's global git config from the host's
// identity and mounts it read-only, plus mounts the global gitignore. This
// REPLACES the old env-forward (`-e YOLO_GIT_*`) + in-jail `git config --global`
// replay: the whole config file is regenerated every run, so a host identity
// that is CHANGED or CLEARED is reflected on the next boot — the old add-only
// setter could never remove a key. Mirrors the gitignore mechanism: a :ro bind
// for podman, acMaterialize (copy) for Apple Container (which has no mount
// namespace for a nested :ro bind).
//
// The gitignore (:ro) is mounted when core.excludesFile (or ~/.config/git/ignore)
// resolves to a real file, and the composed config's core.excludesFile points at
// that in-jail path. With no identity AND no gitignore, nothing is emitted (a
// bare, identity-less jail — preserving the golden argv).
func (o *Options) gitIdentityMountArgs(rt, wsState string, mountTargets map[string]struct{}) []string {
	// Identity uses `--get` (effective config for the host CWD, i.e. repo-local
	// wins) to match the old collectIdentityEnv; the gitignore path stays
	// `--global --get` as before.
	name := o.hostGitConfigGet([]string{"git", "config", "--get", "user.name"})
	email := o.hostGitConfigGet([]string{"git", "config", "--get", "user.email"})

	excludesPath := o.hostGitConfigGet([]string{"git", "config", "--global", "--get", "core.excludesFile"})
	if excludesPath != "" {
		excludesPath = expandUser(excludesPath)
	} else {
		excludesPath = filepath.Join(homeDir(), ".config", "git", "ignore")
	}
	haveIgnore := isFile(excludesPath)

	if name == "" && email == "" && !haveIgnore {
		return nil
	}

	const jailIgnore = "/home/agent/.config/git/ignore"
	var args []string
	if haveIgnore {
		if rt == "container" {
			acMaterialize(excludesPath, ".config/git/ignore", wsState)
		} else {
			args = append(args, ROFileMountArg(
				excludesPath, jailIgnore, wsState, ".config/git/ignore", mountTargets, nil)...)
		}
	}

	excludesInJail := ""
	if haveIgnore {
		excludesInJail = jailIgnore
	}
	content := composeGitconfig(name, email, excludesInJail)
	if rt == "container" {
		// Apple Container mounts the whole wsState at /home/agent, so write the
		// composed file straight into the materialize location (parallels
		// acMaterialize, which copies into <wsState>/<rel>).
		dst := filepath.Join(wsState, ".config", "git", "config")
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		_ = os.WriteFile(dst, []byte(content), 0o644)
	} else {
		staged := filepath.Join(wsState, "yolo-gitconfig")
		_ = os.WriteFile(staged, []byte(content), 0o644)
		args = append(args, "-v", staged+":/home/agent/.config/git/config:ro")
	}
	return args
}

// hostGitConfigGet runs the given `git config … --get <key>` argv on the host,
// returning the trimmed value or "" on any missing-tool / timeout / empty /
// error.
func (o *Options) hostGitConfigGet(argv []string) string {
	res := o.Exec(argv, "", nil, 30*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// composeGitconfig renders the yolo-owned global git config: a [user] section
// (name/email, each omitted when empty) and a [core] excludesFile when the
// gitignore is present. Empty inputs yield a header-only file.
func composeGitconfig(name, email, excludesInJail string) string {
	var b strings.Builder
	b.WriteString("# Auto-generated by yolo-jail from the host git identity.\n")
	b.WriteString("# Regenerated read-only every run — edits here do not persist.\n")
	b.WriteString("# `git config --global` writes to the included file below instead;\n")
	b.WriteString("# this file always wins for the keys it sets.\n")
	if name != "" || email != "" {
		b.WriteString("[user]\n")
		if name != "" {
			b.WriteString("\tname = " + gitConfigValue(name) + "\n")
		}
		if email != "" {
			b.WriteString("\temail = " + gitConfigValue(email) + "\n")
		}
	}
	if excludesInJail != "" {
		b.WriteString("[core]\n")
		b.WriteString("\texcludesFile = " + gitConfigValue(excludesInJail) + "\n")
	}
	// A5: include a WRITABLE sibling so `git config --global` works.
	//
	// Before this, ~/.gitconfig was a DECOY: the symlink resolves to
	// ~/.config/git/config, which is a :ro bind, so `git config --global user.email
	// x` failed with a bare "could not write config file /home/agent/.gitconfig:
	// Device or resource busy" — an error that names a path that looks writable and
	// explains nothing. The composed file already said "edits do not persist", but a
	// user never reaches it: they hit the error through the alias.
	//
	// git applies includes in order and LAST-WINS per key, so placing the include
	// FIRST keeps yolo's identity authoritative for the keys it sets while letting
	// anything else (aliases, pull.rebase, a per-user override) persist in the
	// writable file.
	//
	// Deliberately NOT paired with a GIT_CONFIG_GLOBAL export: that would only be
	// set for shells that source .bashrc, so a git invoked from an agent subprocess
	// or a script with a sanitized env would miss it and fail exactly as before. The
	// include needs no env var — `git config --global` still targets ~/.gitconfig and
	// still fails, but the file it fails on now TELLS the user where to write, which
	// is the legibility this item is about. Making --global itself succeed would mean
	// making ~/.gitconfig writable, which reopens the identity-composition hole that
	// the :ro mount exists to close.
	return gitIncludeHeader() + b.String()
}

// gitLocalConfigInJail is the writable global-config overlay git writes to. It sits
// beside the :ro composed config in ~/.config/git/, which is a writable overlay dir.
const gitLocalConfigInJail = "/home/agent/.config/git/config.local"

// gitIncludeHeader renders the [include] block pulling in the writable sibling.
// Emitted BEFORE the yolo-owned sections so yolo's keys win (git includes are
// applied in file order, last definition wins).
func gitIncludeHeader() string {
	return "[include]\n\tpath = " + gitLocalConfigInJail + "\n"
}

// gitConfigValue renders v as a git-config INI value, quoting only when needed
// (INI-special chars or edge whitespace) so ordinary names/emails stay unquoted,
// matching what `git config` itself writes.
func gitConfigValue(v string) string {
	needQuote := v != strings.TrimSpace(v)
	for i := 0; i < len(v) && !needQuote; i++ {
		switch v[i] {
		case '"', '#', ';', '\\', '\n':
			needQuote = true
		}
	}
	if !needQuote {
		return v
	}
	esc := strings.ReplaceAll(v, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	esc = strings.ReplaceAll(esc, "\n", `\n`)
	return `"` + esc + `"`
}

// forwardHostPortsArgs emits the host-port-forwarding flags:
// the YOLO_FORWARD_HOST_PORTS env + the platform-specific socket wiring
// (--publish-socket for AC, TCP gateway env for macOS podman, -v socket dir for
// Linux). The socat lifecycle itself is separate (network.go).
func (o *Options) forwardHostPortsArgs(rt, cname string, forwardHostPorts []any) []string {
	if len(forwardHostPorts) == 0 {
		return nil
	}
	args := []string{"-e", "YOLO_FORWARD_HOST_PORTS=" + jsonDumps(forwardHostPorts)}
	socketDir := o.fwdSocketDir(cname)
	switch {
	case rt == "container":
		for _, ps := range forwardHostPorts {
			port := strings.SplitN(pyStrCoerce(ps), ":", 2)[0]
			hostSock := filepath.Join(socketDir, "port-"+port+".sock")
			args = append(args, "--publish-socket", hostSock+":/tmp/yolo-fwd/port-"+port+".sock")
		}
	case o.IsMacOS:
		args = append(args, "-e", "YOLO_FWD_HOST_GATEWAY=host.containers.internal")
	default:
		args = append(args, "-v", socketDir+":/tmp/yolo-fwd:rw")
	}
	return args
}

// fwdSocketDir returns /tmp/yolo-fwd-<cname> (resolving /tmp on macOS).
func (o *Options) fwdSocketDir(cname string) string {
	base := "/tmp"
	if o.IsMacOS {
		base = resolvePath("/tmp")
	}
	return filepath.Join(base, "yolo-fwd-"+cname)
}

// hostServicesMountArgs builds the host-services dir mount + the broker relay's
// endpoint env. The broker singleton ensure + relay spawn are side effects handled
// by the lifecycle phase; here we emit the -v and the env var.
//
// THE ENV IS GATED ON THE LOOPHOLE BEING ACTIVE, not on the singleton's socket
// existing at this instant. The container's environment is frozen at `podman run`
// time, so a jail that happened to launch while the singleton was restarting used
// to get NO broker address for its entire life: the in-jail terminator then exits
// 2 and Claude Code will not start, and nothing later can repair it. Loophole
// activity is the same predicate that decides whether the terminator is started at
// all (RuntimeArgsFor's YOLO_JAIL_DAEMONS payload), so the two can no longer
// disagree — and a relay that is late is now a clear "relay unreachable" from the
// terminator rather than a missing variable.
//
// THE ONE SHAPE THAT IS EXCEPTED IS A NESTED LAUNCH WITH NO SINGLETON — see
// brokerEndpointIsUnpublishable, and note that it is NOT the socket gate this
// deliberately replaced.
func (o *Options) hostServicesMountArgs(rt, cname string, cfg *jsonx.OrderedMap) []string {
	if rt == "container" {
		return nil
	}
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	args := []string{"-v", socketsDir + ":" + paths.JailHostServicesDir + ":rw"}
	if brokerLoopholeActive(cfg) && !o.brokerEndpointIsUnpublishable() {
		// A PATH to the 0600 endpoint file. Never an address (the port is
		// kernel-assigned and can change under a running container) and never a
		// token — there is no token environment variable, deliberately: an env var
		// is inherited by every child the terminator spawns.
		args = append(args, "-e",
			hostServiceEnvVar(broker.BrokerLoopholeName)+"="+hostServiceEndpointPath(broker.BrokerLoopholeName))
	}
	return args
}

// brokerEndpointIsUnpublishable reports the one launch shape in which the optimistic
// emission above is a promise NOTHING ON THIS SIDE CAN EVER KEEP: a launcher that is
// itself inside a jail, with no broker singleton listening after brokerEnsure has
// already run and tried to start one (run.go ensures before the argv is built).
//
// # Why a nested launch is different from a host whose broker happens to be down
//
// The singleton is HOST-WIDE, and for a nested launch "the host" is the outer jail.
// yolo's image bakes no openssl, and the broker daemon needs it to mint its CA, so the
// spawn brokerEnsure just performed exits immediately — measured 2026-08-18 in this
// repo's own jail, `yolo-claude-oauth-broker-host: cannot locate openssl`, once per
// launch for months. The socket therefore never appears, run.go skips ensureBrokerRelay
// on exactly this predicate, and nothing is left that could write the endpoint file the
// variable names. The loophole's own CA state files are not in a nested launcher's
// storage either, so the in-jail terminator could not have used the address anyway.
//
// # What that cost once the witness became fatal, which is why this gate is back
//
// Before 2026-08-18 an unbackable promise cost a nested jail its Claude auth, which it
// had already lost. Now it costs the whole jail: a nested launch's disposition is
// `shared` and an endpoint nobody published is faultUnpublished, and BOTH escalate
// (OQ-R4, OQ-R5). MEASURED with a freshly built launcher from inside this jail:
// `yolo -- bash` refused to start, naming claude-oauth-broker — on the one launch shape
// AGENTS.md makes mandatory for verifying a change to cmd/ or internal/. A witness that
// refuses the loop used to fix it is the failure OQ-R2's own implementation note is
// about.
//
// # This is NOT the socket gate that 9b77742 removed
//
// That gate was unconditional, and it was removed for a real defect: a HOST jail
// launched while the singleton was slower to bind than BrokerSpawnTimeout got no broker
// address for its entire frozen life, and a relay that published a second later could
// never repair it. That window is unchanged here — a host launcher still emits the
// variable whether or not the socket is there, which is what
// TestBrokerEnvEmittedWhenLoopholeActive pins, and with it the accepted consequence in
// loopback-tls-reachability.md §7.3 that a host with a dead singleton refuses its jails.
// Only the nested case, where the wait cannot succeed at any timeout because the daemon
// is already gone, is narrowed.
//
// inJail() rather than inContainer(): it is the same YOLO_VERSION signal run.go's other
// host-only decisions read, and it is injectable, so this is testable without a
// container.
func (o *Options) brokerEndpointIsUnpublishable() bool {
	return o.inJail() && !o.PathExists(broker.BrokerSingletonSocket)
}

// brokerLoopholeActive reports whether this launch's broker loophole is enabled, its
// requirements are met, AND the pack that shipped it may touch the host.
//
// Census site 2, through the converged set (loopholes.NewHostSet).
//
// HONORED, NOT Active(), AND THE UPGRADE IS THE PACK MOVE'S OWN CONSEQUENCE. Until
// 2026-08-19 this stopped at Active(), and the reason it could was written down beside
// cgroupDelegateHonored: the broker's record was BUNDLED — yolo's own manifest, in yolo's
// own tree, under a name no pack could claim, so there was no origin to gate. The manifest
// is a contribution of `packs/claude` now (docs/design/broker-as-a-pack.md §10 step 5) and
// the reservation is retired, so both halves of that reason are gone in the same commit:
// the record comes from a pack, and the name is claimable by another one.
//
// What this predicate switches on is not cosmetic — the in-jail TLS terminator, the CA
// mount, the endpoint environment variable, and (through run.go) the host singleton spawn
// itself. Starting all of that on the strength of a pack record whose origin nobody
// evaluated is exactly the crossing the gate exists to govern. For yolo's own official
// `claude` pack the gate passes by construction (an embedded pack carries yolo's own
// authority), so nothing changes for the user this loophole is for.
func brokerLoopholeActive(cfg *jsonx.OrderedMap) bool {
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	if lp, ok := set.Lookup(broker.BrokerLoopholeName); ok {
		return lp.Active() && set.MayRunHostCode(lp)
	}
	return false
}

// deviceArgs builds the device-passthrough args: raw paths, USB by
// vendor:product (resolved via lsusb), and cgroup rules. macOS warns+skips.
func (o *Options) deviceArgs(cfg *jsonx.OrderedMap) []string {
	out := o.pr(o.Stdout)
	var args []string
	for _, devAny := range cfgList(cfg, "devices") {
		switch dev := devAny.(type) {
		case string:
			if o.IsMacOS {
				out.print("[yellow]Warning: device passthrough (" + dev + ") not supported on macOS — skipping[/yellow]")
				continue
			}
			if !o.PathExists(dev) {
				out.print("[yellow]Warning: device " + dev + " not found — skipping[/yellow]")
				continue
			}
			args = append(args, "--device", dev)
		case *jsonx.OrderedMap:
			if usbV, ok := dev.Get("usb"); ok {
				usbID := pyStrCoerce(usbV)
				desc := usbID
				if d := mapStr(dev, "description"); d != "" {
					desc = d
				}
				if o.IsMacOS {
					out.print("[yellow]Warning: USB device passthrough (" + desc + ") not supported on macOS — skipping[/yellow]")
					continue
				}
				args = append(args, o.resolveUSBDevice(usbID, desc)...)
			} else if rule := mapStr(dev, "cgroup_rule"); rule != "" || hasKey(dev, "cgroup_rule") {
				if o.IsMacOS {
					out.print("[yellow]Warning: device cgroup rules not supported on macOS — skipping[/yellow]")
					continue
				}
				args = append(args, "--device-cgroup-rule", mapStr(dev, "cgroup_rule"))
			}
		}
	}
	return args
}

// resolveUSBDevice resolves a USB device via lsusb. Returns the --device args
// (empty on any failure, warned).
func (o *Options) resolveUSBDevice(usbID, desc string) []string {
	out := o.pr(o.Stdout)
	res := o.Exec([]string{"lsusb", "-d", usbID}, "", nil, 5*time.Second)
	if !res.Ran {
		out.print("[yellow]Warning: lsusb not found — cannot resolve USB device IDs[/yellow]")
		return nil
	}
	if res.Timeout || res.RC != 0 || strings.TrimSpace(res.Stdout) == "" {
		out.print("[yellow]Warning: USB device " + desc + " (" + usbID + ") not found — skipping[/yellow]")
		return nil
	}
	line := strings.SplitN(strings.TrimSpace(res.Stdout), "\n", 2)[0]
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return nil
	}
	bus := parts[1]
	device := strings.TrimRight(parts[3], ":")
	devPath := "/dev/bus/usb/" + bus + "/" + device
	if !o.PathExists(devPath) {
		out.print("[yellow]Warning: USB device " + desc + " found by lsusb but " + devPath + " missing — skipping[/yellow]")
		return nil
	}
	out.print("[dim]USB device: " + desc + " → " + devPath + "[/dim]")
	return []string{"--device", devPath}
}

// kvmArgs builds the KVM passthrough block. keepGroupsAlready
// reports whether the assembled command already carries --group-add
// keep-groups (the ROCm block adds it on podman): podman rejects keep-groups
// combined with any other --group-add value, INCLUDING a duplicate of itself,
// so the kvm block must not add a second copy (AMD GPU + kvm together).
func (o *Options) kvmArgs(cfg *jsonx.OrderedMap, rt string, keepGroupsAlready bool) []string {
	if !cfgTrue(cfg, "kvm") {
		return nil
	}
	out := o.pr(o.Stdout)
	if o.IsMacOS || rt == "container" {
		out.print("[yellow]Warning: kvm passthrough is not supported on this runtime — skipping[/yellow]")
		return nil
	}
	if !o.PathExists("/dev/kvm") {
		out.print("[yellow]Warning: /dev/kvm not present on host — skipping kvm passthrough[/yellow]")
		return nil
	}
	args := []string{"--device", "/dev/kvm"}
	if rt == "podman" && !keepGroupsAlready {
		args = append(args, "--group-add", "keep-groups")
	}
	out.print("[dim]KVM passthrough: /dev/kvm[/dim]")
	return args
}

// userConfigMountArgs MOVED to inheritscope.go, where the inner user scope is now
// GENERATED per consumer instead of raw-bound from the human's real config (OQ-LP9).

// loopholesRuntimeArgs builds the host-side loopholes runtime args:
// --add-host, CA cert mounts, NODE_EXTRA_CA_CERTS.
//
// Census site 3, through the converged set. Enabled() rather than All() keeps the argv
// byte-identical to what a hand-built Discover(IncludeDisabled:false) produced; the
// distinction is moot for the output either way (RuntimeArgsFor's own loop skips anything
// not Active()) and is kept because the ARGV is golden-tested.
// THE SET'S RuntimeArgsFor, not the package-level one, and that is the origin gate's
// enforcement half (§4.3 G3): the package function honors no SourcePack record at all,
// because a slice carries no gate. Going through the Set is how this call site says it
// evaluated one — an unapproved fetched pack's binds, devices, intercepts and CA are then
// dropped here rather than reaching the container.
func (o *Options) loopholesRuntimeArgs(cfg *jsonx.OrderedMap, rt string) []string {
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	return set.RuntimeArgsFor(set.Enabled(), rt)
}

// hasKey reports whether m has key (present, even if the value is falsy).
func hasKey(m *jsonx.OrderedMap, key string) bool {
	_, ok := m.Get(key)
	return ok
}

// resourceArgs builds the resource-limits block: --memory/--cpus with
// Apple-Container defaults, and --pids-limit (podman default 32768).
//
// The DECISION — which flags this backend passes and with what values — is
// appliedResourceLimits, because the briefing states the same list in prose and the two
// must be one answer (backend-parity.md §6). This function is only its argv spelling.
func (o *Options) resourceArgs(cfg *jsonx.OrderedMap, rt string) []string {
	var args []string
	for _, lim := range appliedResourceLimits(rt, cfgMap(cfg, "resources"), o.appleContainerDefaultMemory) {
		args = append(args, lim.flag, lim.value)
	}
	return args
}

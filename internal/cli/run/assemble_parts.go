package run

import (
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
	// storage.EnsureGlobalStorage MkdirAlls every EmbeddedSharedDirs() under GlobalHome
	// on every backend, and runContainer's ensureSharedDirSources the selected packs' own (a
	// configured pack's included) on both container backends, so the host side exists
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

// podmanBaseMounts builds the podman base mounts: this jail's :ro home skeleton +
// the per-workspace writable overlays (dirs, files) + the mise store mount
// (named volume on macOS, bind dir otherwise).
// isMacOS comes from the Options seam, never paths.IsMacOS, so the golden argv
// is the same on every host.
//
// THE HOME ROOT IS PER JAIL. It was paths.GlobalHome(), one machine-wide base every podman
// jail shared, which is how one workspace's pack dirs, host_files links and old bytes — and
// a claude-less jail's view of the machine's Claude credential file — reached every jail.
// It is now in.homeSkeleton, built from THIS launch's selected packs and config
// (buildHomeSkeleton, docs/design/base-home-legacy-state.md#2-the-design-a-per-jail-skeleton).
// Still bound :ro, so writing an undeclared home path fails with EROFS as before.
func podmanBaseMounts(rt string, runFlags []string, workspace string, in *assembleInput, isMacOS bool) []string {
	ws := in.wsState
	runCmd := append([]string{rt, "run"}, runFlags...)
	runCmd = append(runCmd,
		"-v", workspace+":/workspace",
		"-v", in.homeSkeleton+":/home/agent:ro",
		"-v", filepath.Join(ws, "npm-global")+":/home/agent/.npm-global",
		"-v", filepath.Join(ws, "local")+":/home/agent/.local",
		"-v", filepath.Join(ws, "go")+":/home/agent/go",
		// ONE anchor for BOTH generated-script dirs. The entrypoint writes
		// ~/.yolo/bin/{block,launch} every boot and /home/agent is :ro, so without a rw
		// bind the boot fails EROFS — but they need only ONE, because they are subdirs of
		// a common parent. They stay separate DIRECTORIES because blockers must precede
		// the real tool on PATH and lazy installers must not (see entrypoint.Env.LaunchDir);
		// gathering them in the filesystem is not gathering them on PATH, and nothing may
		// ever put this parent on PATH.
		"-v", filepath.Join(ws, "yolo-bin")+":/home/agent/.yolo/bin",
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
	// L9's host-CAS aliases (OQ-BF10): the same shape as the relocations above —
	// a rw bind nested inside the .cache mount — with two differences that are the
	// whole feature. The SOURCE is the host user's own cache rather than storage
	// the user nominated, and the DESTINATION is the path the jail's copy of the
	// tool already uses, so the mount lands on top of the private copy instead of
	// beside it. Emitted here purely for readability, exactly as above: podman
	// sorts mounts by destination depth.
	//
	// It is deliberately AFTER the relocations, for readability only: the two can
	// never both target one subtree, because hostcas DECLINES a store whose cache
	// segment the user relocated (CodeRelocated). That gate is not tidiness — both
	// mounts would otherwise apply, the deeper alias winning for its own subtree,
	// and a user who moved `pants` to get 40 G off their home disk would silently
	// get 27 G of it back.
	runCmd = append(runCmd, hostCASAliasArgs(in.hostCASAlias)...)
	runCmd = append(runCmd,
		"-v", filepath.Join(ws, "yolo-bootstrap.sh")+":/home/agent/.yolo-bootstrap.sh",
		"-v", filepath.Join(ws, "yolo-venv-precreate.sh")+":/home/agent/.yolo-venv-precreate.sh",
		"-v", filepath.Join(ws, "yolo-perf.log")+":/home/agent/.yolo-perf.log",
		"-v", filepath.Join(ws, "yolo-socat.log")+":/home/agent/.yolo-socat.log",
		"-v", filepath.Join(ws, "yolo-entrypoint.lock")+":/home/agent/.yolo-entrypoint.lock",
		"-v", filepath.Join(ws, "yolo-ca-bundle.crt")+":/home/agent/.yolo-ca-bundle.crt",
		"-v", filepath.Join(ws, "bash_history")+":/home/agent/.bash_history",
		"-v", filepath.Join(ws, "ssh")+":/home/agent/.ssh",
	)
	// Writable home dirs: extra $HOME subpaths (config writable_home_dirs) made
	// read-write by nesting a bind INSIDE the :ro home skeleton. The OCI
	// runtime does NOT auto-create mountpoints inside a :ro bind mount (crun
	// mkdirat fails with EROFS) — existing mounts (.npm-global etc.) work only
	// because those dirs already exist in the skeleton. buildHomeSkeleton creates
	// the mountpoint there for each declared entry. Sorted for a
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
//
// THE NESTING BRANCH IS FIRST AND MUST STAY FIRST — a doubly-nested user namespace
// fails mounting /proc, so `--userns host` is not optional — which means a nested
// launch can never reach the NVIDIA branch below. That used to leave the two halves of
// NVIDIA passthrough split across the argv (`--runtime runc` dropped here, the CDI
// device flags still emitted by gpuArgs); the caller now declines nested NVIDIA before
// the probe answers, so gpuEnabled is false by the time this is reached and the split
// is unrepresentable rather than merely unlikely.
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
	// Three `git config` execs, spanned together: the other half of what made
	// argv assembly cost seconds on a real host.
	gsp := o.Perf.Span("assemble.host_git_identity")
	defer gsp.End()
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
	// Both writes are BENEATH wsState (writeFileBeneath, wsstatebeneath.go), never a plain path
	// write: the jail can leave a link at either file, or above the Apple Container one, for this
	// write to follow onto a host file, and podman would then bind the link's target.
	if rt == "container" {
		// Apple Container mounts the whole wsState at /home/agent, so write the
		// composed file straight into the materialize location (parallels
		// acMaterialize, which copies into <wsState>/<rel>).
		_ = writeFileBeneath(wsState, filepath.Join(".config", "git", "config"), []byte(content), 0o644)
	} else {
		staged := filepath.Join(wsState, "yolo-gitconfig")
		_ = writeFileBeneath(wsState, "yolo-gitconfig", []byte(content), 0o644)
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

// hostServicesMountArgs builds the host-services dir mount and the endpoint env of
// every host-scoped loophole this launch can back. Singleton ensure + front spawn are
// side effects handled by the lifecycle phase; here we emit the -v and the env vars.
//
// THE SET IS DERIVED, NEVER LISTED. It used to be two `if`s naming
// `claude-oauth-broker` and `openai-auth-broker`, and the third host-scoped loophole
// ever shipped — `aws-auth` — therefore got NO variable at all, so its in-jail adapter
// answered `ServiceUnreachable` for every request while the launch reported a healthy
// jail: the reachability witness walks YOLO_SERVICE_*_ENDPOINT, and the fault was that
// no such variable existed for it to walk. A third name would have been the same defect
// with a longer fuse. What the two branches actually computed — ACTIVE and MAY-RUN-HOST-
// CODE — is a predicate, and hostScopedEndpoints applies it to every record declaring
// `host_daemon.scope: "host"`. `rg -n '"scope": "host"' packs/*/loopholes/*/manifest.jsonc`
// is the whole census; nothing here counts it or spells it.
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
// THE ONE SHAPE THAT IS EXCEPTED is Apple Container, where two independent halves of
// the pipeline decline to publish at all — hostScopedEndpointIsUnpublishable's, and note
// that it is not the socket gate this deliberately replaced.
//
// THE EARLY RETURN IS DERIVED TOO, and it was the same defect wearing a different hat:
// it read `rt == "container" && !openAIAuthLoopholeActive(cfg)`, one hardcoded name
// standing in for the question "will anything publish into this directory on this
// backend?". Asked directly — is the publishable set empty? — it answers the same for
// the AC launches that produced the old spelling (that backend's allow list admits the
// OpenAI service alone, so an AC launch without it publishes nothing anywhere) and it
// stops being a name. It stays scoped to `rt == "container"`: on every other backend
// the directory also carries JAIL-scoped daemons' endpoint files, whose variables are
// emitted elsewhere, so the mount is owed there even when this set is empty.
func (o *Options) hostServicesMountArgs(rt, cname string, cfg *jsonx.OrderedMap) []string {
	names := hostScopedEndpoints(rt, cfg)
	if rt == "container" && len(names) == 0 {
		return nil
	}
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	args := []string{"-v", socketsDir + ":" + paths.JailHostServicesDir + ":rw"}
	for _, name := range names {
		// A PATH to the 0600 endpoint file. Never an address (the port is
		// kernel-assigned and can change under a running container) and never a
		// token — there is no token environment variable, deliberately: an env var
		// is inherited by every child the terminator spawns.
		args = append(args, "-e", hostServiceEnvVar(name)+"="+hostServiceEndpointPath(name))
	}
	return args
}

// hostScopedEndpoints names the loopholes whose endpoint variable this launch owes the
// jail: every record declaring `host_daemon.scope: "host"` that is Active AND whose pack
// may run host code, minus the ones this launch shape cannot publish.
//
// Census site 2, through the converged set (loopholes.NewHostSet) — the same site
// brokerLoopholeActive used to hold on its own, which is why that predicate now reads
// this list instead of building a second view of the same machine.
//
// SCOPE IS READ FROM THE MANIFEST, through loopholes.ScopeHost, and compared against
// ScopeHost rather than against ScopeJail for the reason startHostSingleton's own loop
// gives: the FIELD's zero value is "", not ScopeJail, so a dropped Scope must cost a
// variable rather than silently promise a per-jail daemon's endpoint under a host-wide
// name. It is the same comparison the SPAWN makes (loopholesruntime.go), which is what
// keeps "yolo starts a host-wide daemon for this" and "the jail is told where it is"
// from being two independently maintained answers.
//
// HONORED, NOT Active(): the records come from PACKS, and starting a host-side listener
// — or pointing a jail at one — on the strength of a pack record whose origin nobody
// evaluated is exactly the crossing MayRunHostCode exists to govern. For yolo's own
// official packs the gate passes by construction.
//
// Discovery order, not sorted: it is the order every other view of this Set uses, so an
// argv diff between two surfaces is a real difference rather than a collation one.
func hostScopedEndpoints(rt string, cfg *jsonx.OrderedMap) []string {
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	var names []string
	for _, lp := range set.Active() {
		if lp.HostDaemon == nil || lp.HostDaemon.Scope != loopholes.ScopeHost {
			continue
		}
		if !set.MayRunHostCode(lp) {
			continue
		}
		if hostScopedEndpointIsUnpublishable(rt, lp.Name) {
			continue
		}
		names = append(names, lp.Name)
	}
	return names
}

// hostScopedEndpointIsUnpublishable reports the launch shapes in which the optimistic
// emission above is a promise NOTHING ON THIS SIDE CAN EVER KEEP.
//
// ONE SHAPE IS LEFT, and the deletion of the other is this function's whole recent
// history. It had two arms on different axes: a launcher itself inside a jail with no
// broker singleton listening after brokerEnsure had already tried, and the Apple
// Container backend on every launch. The nested arm is GONE (2026-09-20), and it is
// gone rather than reworded because both halves of what justified it are spent:
//
//   - "yolo's image bakes no openssl, and the broker daemon needs it to mint its CA, so
//     the spawn brokerEnsure just performed exits immediately" — measured 2026-08-18 in
//     this repo's own jail, `yolo-claude-oauth-broker-host: cannot locate openssl`, once
//     per launch for months. `openssl` was baked (`431625bc`), and then `EnsureCAAndLeaf`
//     stopped needing it at all (`d5bb1e5d`, in-process crypto/x509).
//   - "the loophole's own CA state files are not in a nested launcher's storage, so the
//     in-jail terminator could not have used the address anyway." MEASURED 2026-09-18: a
//     nested launch minted its OWN P-256 CA in its OWN state directory, mounted the trio,
//     and published /run/yolo-services/claude-oauth-broker.endpoint — `openssl verify
//     -verify_hostname platform.claude.com` returned OK against the real mounted files
//     (docs/reference/claude-oauth-interposition.md#a-nested-jail-runs-its-own-broker). A nested jail runs its own broker;
//     it does not borrow its launcher's.
//
// `OQ-2` ruled exactly that — nesting earns affordances, not exemptions, and a jail that
// behaves differently cannot test the thing it is nested inside — so a nested launch is
// now an ordinary launch here, wired whether or not the launcher's own singleton is up.
// That is the same treatment the HOST already got and for the same reason (below).
//
// # This is NOT the socket gate that 9b77742 removed
//
// That gate was unconditional, and it was removed for a real defect: a HOST jail
// launched while the singleton was slower to bind than BrokerSpawnTimeout got no broker
// address for its entire frozen life, and a relay that published a second later could
// never repair it. That window is unchanged here — a launcher still emits the variable
// whether or not the socket is there, which is what TestBrokerEnvEmittedWhenLoopholeActive
// pins, and with it the accepted consequence in loopback-tls-reachability.md §7.3 that a
// host with a dead singleton refuses its jails. With the nested arm gone, nothing on this
// path reads a socket at all.
//
// # Apple Container has no publisher at all, and no timing to wait out
//
// This arm is not a race: NOTHING in an AC launch is even asked to write the endpoint
// file for anything but the one service its allow list admits. run.go ensures the
// host-wide singleton only when `rt != "container"`, and startLoopholes' per-runtime
// allow list admits `openai-auth-broker` alone on this backend — so for every other
// host-scoped loophole both the daemon and the front that would publish for it are
// absent by construction, not late. The jail nevertheless got a path under
// JailHostServicesDir whenever the broker loophole was active, which on AC means
// `packs: ["claude", "codex"]`: codex brings the OpenAI loophole, claude brings the
// broker record. The in-jail terminator then dials a file that never appears, and since
// the witness became fatal that is faultUnpublished — it can refuse the whole launch
// (OQ-R4, OQ-R5), for a service this backend was never going to run.
//
// Suppressing the variable does not cost AC anything it had: the broker's CA state and
// its relay are equally absent there, so no jail ever completed a refresh through it.
//
// ⚠ THE NAME BELOW IS THE BACKEND'S ALLOW LIST, NOT A CENSUS ENTRY. The set of
// host-scoped loopholes is derived (hostScopedEndpoints); which of them Apple Container
// STARTS is a per-backend fact owned by startLoopholes, and this is the second spelling
// of it. TestAppleContainerAllowListHasOneSpelling ties the two together at the source,
// so widening that allow list without widening this reintroduces the defect one backend
// over.
func hostScopedEndpointIsUnpublishable(rt, name string) bool {
	if rt == "container" { // parity: Warned — no publisher exists on AC for anything but the OpenAI service (no singleton ensure, allow list is openai-auth alone), and notePackLoopholesInert names the loophole at launch
		return name != openAIAuthBrokerName
	}
	return false
}

// brokerLoopholeActive reports whether this launch's broker loophole is enabled, its
// requirements are met, AND the pack that shipped it may touch the host — the gate
// run.go's singleton ensure reads.
//
// IT IS THE DERIVED LIST'S MEMBERSHIP TEST NOW, not a second view of the same machine.
// It used to build its own loopholes.NewHostSet and ask Lookup + Active + MayRunHostCode
// — which was correct, and was also exactly the computation hostScopedEndpoints performs
// for every host-scoped record. Two spellings of one predicate are two that can disagree,
// and "the spawn and the wiring are governed by ONE predicate" is the property
// TestBrokerLifecycleIsGatedOnTheLoopholeRecord exists to hold.
//
// ⚠ IT PASSES "" AS THE RUNTIME, deliberately: the spawn gate asks whether the loophole
// is ON, and run.go's caller already carries the backend half (`rt != "container"`).
// Passing a real rt here would fold the AC suppression into the SPAWN decision, which is
// a different question from "may this pack's host code run at all".
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
	return inStrSlice(hostScopedEndpoints("", cfg), broker.BrokerLoopholeName)
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
// --add-host, CA cert mounts, NODE_EXTRA_CA_CERTS — plus the YOLO_JAIL_DAEMONS
// env, whose payload is COMPOSED ABOVE THE BACKEND DISPATCH and threaded in
// (jailDaemonsFor; wire-bridge.md §2.1 — one env contract, one writer). This
// call site serializes that one value rather than composing a second copy of
// it, which is what lets the native backend read the same payload it can only
// decline (docs/design/jail-daemon-on-macos-user-plan.md).
//
// Census site 3, through the converged set. Enabled() rather than All() keeps the argv
// byte-identical to what a hand-built Discover(IncludeDisabled:false) produced; the
// distinction is moot for the output either way (RuntimeArgsFor's own loop skips anything
// not Active()) and is kept because the ARGV is golden-tested.
// A SET METHOD, not the package-level function, and that is the origin gate's enforcement
// half (§4.3 G3): the package function honors no SourcePack record at all, because a slice
// carries no gate. Going through the Set is how this call site says it evaluated one — an
// unapproved fetched pack's binds, devices, intercepts and CA are then dropped here rather
// than reaching the container. Services need no gate of their own: a service crosses no
// boundary (kinds.go's anti-loophole), so there is nothing on this path for an origin gate
// to withhold. The hoisted payload was composed through the same gate, by the same Set
// constructor, so threading it past this one withholds nothing either.
func (o *Options) loopholesRuntimeArgs(cfg *jsonx.OrderedMap, rt string,
	jailDaemons []loopholes.JailDaemonSpec) []string {
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	return set.RuntimeArgsWithJailDaemons(set.Enabled(), rt, jailDaemons)
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

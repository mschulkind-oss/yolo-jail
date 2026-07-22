package run

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
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
func appleContainerBaseMounts(rt string, runFlags []string, workspace string, in *assembleInput, out printer) []string {
	wsState := in.wsState
	if len(in.cacheRelocations) > 0 {
		out.print("[yellow]Skipping cache_relocations (" + cacheRelocationSubdirs(in.cacheRelocations) +
			"): cache_relocations are not implemented on Apple Container, " +
			"so the cache stays on its original filesystem. " +
			"Use `YOLO_RUNTIME=podman` for cache relocation.[/yellow]")
	}
	runCmd := append([]string{rt, "run"}, runFlags...)
	return append(runCmd,
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
	// mise store: named volume on macOS, bind dir otherwise.
	if isMacOS {
		runCmd = append(runCmd, "-v", miseStoreVolume+":/mise")
	} else {
		runCmd = append(runCmd, "-v", in.miseStore+":/mise")
	}
	return runCmd
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

// gitignoreMountArgs handles global-gitignore propagation: read
// core.excludesFile (or ~/.config/git/ignore), mount it :ro (dereferencing a
// nested bind), and set YOLO_GLOBAL_GITIGNORE. Apple Container materializes into
// ws_state instead of mounting.
func (o *Options) gitignoreMountArgs(rt, wsState string, mountTargets map[string]struct{}) []string {
	var excludesPath string
	res := o.Exec([]string{"git", "config", "--global", "--get", "core.excludesFile"}, "", nil, 30*time.Second)
	if res.Ran && !res.Timeout && res.RC == 0 {
		ef := strings.TrimSpace(res.Stdout)
		if ef != "" {
			excludesPath = expandUser(ef)
		} else {
			excludesPath = filepath.Join(homeDir(), ".config", "git", "ignore")
		}
	} else {
		excludesPath = filepath.Join(homeDir(), ".config", "git", "ignore")
	}
	if !isFile(excludesPath) {
		return nil
	}
	var args []string
	if rt == "container" {
		acMaterialize(excludesPath, ".config/git/ignore", wsState)
	} else {
		args = append(args, ROFileMountArg(
			excludesPath, "/home/agent/.config/git/ignore", wsState, ".config/git/ignore", mountTargets, nil)...)
	}
	args = append(args, "-e", "YOLO_GLOBAL_GITIGNORE=/home/agent/.config/git/ignore")
	return args
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

// hostServicesMountArgs builds the host-services sockets-dir mount + broker relay
// env. The broker singleton ensure + relay spawn are side effects
// handled by the lifecycle phase; here we emit the -v + the broker socket env
// when the singleton socket exists.
func (o *Options) hostServicesMountArgs(rt, cname string) []string {
	if rt == "container" {
		return nil
	}
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	args := []string{"-v", socketsDir + ":" + paths.JailHostServicesDir + ":rw"}
	if o.PathExists(broker.BrokerSingletonSocket) {
		brokerJailSock := paths.JailHostServicesDir + "/" + broker.BrokerLoopholeName + ".sock"
		args = append(args, "-e", hostServiceEnvVar(broker.BrokerLoopholeName)+"="+brokerJailSock)
	}
	return args
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

// repoMountSource selects the /opt/yolo-jail source, returning ok=false on a
// DEGRADED launch (D2) where there is no source tree to bind: repoRoot=="" AND
// the workspace isn't itself a yolo-jail checkout. In that case the caller omits
// both the /opt bind and YOLO_REPO_ROOT so the in-jail CLI doesn't trust an
// empty/foreign mount as the repo.
func (o *Options) repoMountSource(repoRoot string) (string, bool) {
	if workspaceIsYoloSourceTree(o.Workspace) {
		return o.Workspace, true
	}
	if repoRoot == "" {
		return "", false
	}
	if fileExists(filepath.Join(repoRoot, "flake.nix")) {
		return repoRoot, true
	}
	return o.Workspace, true
}

// userConfigMountArgs builds the user-config mount for nested jails.
func (o *Options) userConfigMountArgs(rt, wsState string, mountTargets map[string]struct{}) []string {
	userPath := paths.UserConfigPath()
	if !isFile(userPath) {
		return nil
	}
	name := filepath.Base(userPath)
	containerConfig := "/home/agent/.config/yolo-jail/" + name
	relConfig := ".config/yolo-jail/" + name
	if rt == "container" {
		acMaterialize(userPath, relConfig, wsState)
		return nil
	}
	return ROFileMountArg(userPath, containerConfig, wsState, relConfig, mountTargets, nil)
}

// loopholesRuntimeArgs builds the host-side loopholes runtime args:
// --add-host, CA cert mounts, NODE_EXTRA_CA_CERTS.
func (o *Options) loopholesRuntimeArgs(cfg *jsonx.OrderedMap, rt string) []string {
	discovered := loopholes.Discover(loopholes.DiscoverOptions{
		IncludeBundled:  true,
		LoopholesConfig: cfgMap(cfg, "loopholes"),
	})
	return loopholes.RuntimeArgsFor(discovered, rt)
}

// hasKey reports whether m has key (present, even if the value is falsy).
func hasKey(m *jsonx.OrderedMap, key string) bool {
	_, ok := m.Get(key)
	return ok
}

// resourceArgs builds the resource-limits block: --memory/--cpus with
// Apple-Container defaults, and --pids-limit (podman default 32768).
func (o *Options) resourceArgs(cfg *jsonx.OrderedMap, rt string) []string {
	resCfg := cfgMap(cfg, "resources")
	var args []string
	var memory string
	var cpus string
	haveCPUs := false
	if resCfg != nil {
		if v := mapGet(resCfg, "memory"); v != nil {
			memory = pyStrCoerce(v)
		}
		if v := mapGet(resCfg, "cpus"); v != nil {
			cpus = pyStrCoerce(v)
			haveCPUs = true
		}
	}

	if rt == "container" {
		if !haveCPUs {
			hostCPUs := numCPU()
			half := hostCPUs / 2
			if half < 2 {
				half = 2
			}
			cpus = strconv.Itoa(half)
			haveCPUs = true
		}
		if memory == "" {
			memory = o.appleContainerDefaultMemory()
		}
	}

	if memory != "" {
		args = append(args, "--memory", memory)
	}
	if haveCPUs {
		args = append(args, "--cpus", cpus)
	}
	if rt != "container" {
		pids := "32768"
		if resCfg != nil {
			if v := mapGet(resCfg, "pids_limit"); v != nil {
				pids = pyStrCoerce(v)
			}
		}
		args = append(args, "--pids-limit", pids)
	}
	return args
}

package checkcmd

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// runtimeForCheck ports _runtime_for_check: resolve the effective runtime
// without exiting. Returns (runtime, errorMessage). A native (macos-user)
// selection short-circuits before any which/probe. Only returns container
// runtimes whose daemon is actually reachable.
func (o *Options) runtimeForCheck(config *jsonx.OrderedMap) (string, string) {
	env := o.Getenv("YOLO_RUNTIME")
	if env != "" && inStrSlice(paths.AllRuntimes, env) {
		if rt, errMsg, native := o.nativeRuntimeCheck(env, "YOLO_RUNTIME"); native {
			return rt, errMsg
		}
		if _, ok := o.LookPath(env); ok {
			if o.runtimeIsConnectable(env) {
				return env, ""
			}
			return "", "Configured runtime '" + env + "' from YOLO_RUNTIME is not connected"
		}
		return "", "Configured runtime '" + env + "' from YOLO_RUNTIME is not on PATH"
	}

	cfg := configRuntime(config)
	if cfg != "" && inStrSlice(paths.AllRuntimes, cfg) {
		if rt, errMsg, native := o.nativeRuntimeCheck(cfg, "yolo-jail.jsonc"); native {
			return rt, errMsg
		}
		if _, ok := o.LookPath(cfg); ok {
			if o.runtimeIsConnectable(cfg) {
				return cfg, ""
			}
			return "", "Configured runtime '" + cfg + "' from yolo-jail.jsonc is not connected"
		}
		return "", "Configured runtime '" + cfg + "' from yolo-jail.jsonc is not on PATH"
	}

	var candidates []string
	if o.IsMacOS {
		candidates = []string{"container", "podman"}
	} else {
		candidates = []string{"podman"}
	}
	for _, rt := range candidates {
		path, ok := o.LookPath(rt)
		if !ok {
			continue
		}
		if rt == "container" && !o.isAppleContainer(path) {
			continue
		}
		if !o.runtimeIsConnectable(rt) {
			continue
		}
		return rt, ""
	}
	return "", "No container runtime found on PATH"
}

// nativeRuntimeCheck ports _native_runtime_check. The third return is true when
// rt is a native runtime (the caller should use the returned rt/errMsg); false
// means "not native, continue normal resolution".
func (o *Options) nativeRuntimeCheck(rt, source string) (string, string, bool) {
	if !inStrSlice(paths.NativeRuntimes, rt) {
		return "", "", false
	}
	if !o.IsMacOS {
		return "", "Runtime '" + rt + "' from " + source + " is macOS-only " +
			"(native user + Seatbelt); this host is not macOS.", true
	}
	return rt, "", true
}

// runtimeIsConnectable ports _runtime_is_connectable: does the daemon answer?
func (o *Options) runtimeIsConnectable(rt string) bool {
	if rt == "container" {
		res := o.Exec([]string{"container", "system", "status"}, "", nil, 5*time.Second)
		if !res.Ran || res.Timeout {
			return false
		}
		return res.RC == 0 && strings.Contains(strings.ToLower(res.Stdout), "running")
	}
	res := o.Exec([]string{rt, "info"}, "", nil, 10*time.Second)
	if !res.Ran || res.Timeout {
		return false
	}
	return res.RC == 0
}

// isAppleContainer ports _is_apple_container: is the binary Apple's container
// CLI (not some other `container`)?
func (o *Options) isAppleContainer(path string) bool {
	res := o.Exec([]string{path, "--version"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return false
	}
	out := res.Stdout + res.Stderr
	return strings.Contains(out, "Apple") || strings.Contains(out, "container CLI version")
}

// detectRuntimeForListing ports _detect_runtime_for_listing: first SUPPORTED
// runtime on PATH, or "".
func (o *Options) detectRuntimeForListing() string {
	for _, rt := range paths.SupportedRuntimes {
		if _, ok := o.LookPath(rt); ok {
			return rt
		}
	}
	return ""
}

// detectRuntime ports _detect_runtime: YOLO_RUNTIME or "podman".
func (o *Options) detectRuntime() string {
	if v := o.Getenv("YOLO_RUNTIME"); v != "" {
		return v
	}
	return "podman"
}

// listRunningJailNames ports list_running_jail_names: (names, errorMessage).
// errorMessage is non-empty only when listing genuinely failed.
func (o *Options) listRunningJailNames(rt string) ([]string, string) {
	if rt == "container" {
		res := o.Exec([]string{"container", "ls"}, "", nil, 5*time.Second)
		if !res.Ran {
			return nil, "exec failed"
		}
		if res.Timeout {
			return nil, "timeout"
		}
		if res.RC != 0 {
			if e := strings.TrimSpace(res.Stderr); e != "" {
				return nil, e
			}
			return nil, "container ls failed"
		}
		return runtime.ParseContainerLsNames(res.Stdout), ""
	}
	res := o.Exec([]string{rt, "ps", "--filter", "name=^yolo-", "--format", "{{.Names}}"}, "", nil, 5*time.Second)
	if !res.Ran {
		return nil, "exec failed"
	}
	if res.Timeout {
		return nil, "timeout"
	}
	if res.RC != 0 {
		if e := strings.TrimSpace(res.Stderr); e != "" {
			return nil, e
		}
		return nil, rt + " ps failed"
	}
	return runtime.ParseRunningJailNames(res.Stdout), ""
}

// getContainerWorkspace ports _get_container_workspace: the tracking file first
// (fast), then the runtime inspect env fallback, else "unknown".
func (o *Options) getContainerWorkspace(name, rt string) string {
	if ws, ok := runtime.ReadContainerWorkspace(name); ok {
		return ws
	}
	if rt == "container" {
		res := o.Exec([]string{"container", "inspect", name}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout && res.RC == 0 {
			if ws, ok := parseAppleInspectWorkspace(res.Stdout); ok {
				return ws
			}
		}
		return "unknown"
	}
	res := o.Exec([]string{rt, "inspect", name, "--format", "{{range .Config.Env}}{{println .}}{{end}}"}, "", nil, 5*time.Second)
	if res.Ran && !res.Timeout && res.RC == 0 {
		lines := strings.Split(res.Stdout, "\n")
		if ws, ok := runtime.WorkspaceFromInspectEnv(lines); ok {
			return ws
		}
	}
	return "unknown"
}

// parseAppleInspectWorkspace mirrors the Apple-Container JSON branch of
// _get_container_workspace: data["config"]["env"] scanned for YOLO_HOST_DIR=.
func parseAppleInspectWorkspace(stdout string) (string, bool) {
	decoded, err := jsonx.Decode([]byte(stdout))
	if err != nil {
		return "", false
	}
	obj, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return "", false
	}
	cfgV, _ := obj.Get("config")
	cfg, ok := cfgV.(*jsonx.OrderedMap)
	if !ok {
		return "", false
	}
	envV, _ := cfg.Get("env")
	envList, ok := envV.([]any)
	if !ok {
		return "", false
	}
	for _, e := range envList {
		if s, ok := e.(string); ok && strings.HasPrefix(s, "YOLO_HOST_DIR=") {
			return s[len("YOLO_HOST_DIR="):], true
		}
	}
	return "", false
}

// checkContainerStuck ports _check_container_stuck: returns a reason string if
// stuck, "" if healthy (or the runtime has no `top`).
func (o *Options) checkContainerStuck(name, rt string) string {
	if rt == "container" {
		return ""
	}
	res := o.Exec([]string{rt, "top", name, "-eo", "comm"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return ""
	}
	return runtime.StuckReasonFromTop(res.Stdout)
}

// podmanMachineMemory ports _podman_machine_memory: (name, memMB, ok).
func (o *Options) podmanMachineMemory() (string, int, bool) {
	res := o.Exec([]string{"podman", "machine", "inspect"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 || strings.TrimSpace(res.Stdout) == "" {
		return "", 0, false
	}
	decoded, err := jsonx.Decode([]byte(res.Stdout))
	if err != nil {
		return "", 0, false
	}
	machines, ok := decoded.([]any)
	if !ok || len(machines) == 0 {
		return "", 0, false
	}
	// Prefer a running machine; else the first (if it is a dict).
	var machine *jsonx.OrderedMap
	for _, m := range machines {
		mm, ok := m.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		if st, _ := mm.Get("State"); asString(st) == "running" {
			machine = mm
			break
		}
	}
	if machine == nil {
		if mm, ok := machines[0].(*jsonx.OrderedMap); ok {
			machine = mm
		}
	}
	if machine == nil {
		return "", 0, false
	}
	resV, _ := machine.Get("Resources")
	resources, ok := resV.(*jsonx.OrderedMap)
	if !ok {
		// `machine.get("Resources") or {}` — a missing/None Resources yields no
		// Memory, so this is not a valid reading.
		return "", 0, false
	}
	memV, _ := resources.Get("Memory")
	memMB, ok := jsonx.AsInt(memV)
	if !ok || memMB <= 0 {
		return "", 0, false
	}
	name := asString(getFirst(machine, "Name"))
	if name == "" {
		name = "podman-machine-default"
	}
	return name, int(memMB), true
}

func getFirst(m *jsonx.OrderedMap, key string) any {
	v, _ := m.Get(key)
	return v
}

// configRuntime returns config["runtime"] as a string, or "".
func configRuntime(config *jsonx.OrderedMap) string {
	if config == nil {
		return ""
	}
	v, _ := config.Get("runtime")
	return asString(v)
}

func asString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func inStrSlice(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// resolveRepoRoot ports the pieces of _resolve_repo_root that check() relies on:
// the YOLO_REPO_ROOT env var (validated to contain source) and a
// source-checkout / installed-package fallback. Returns (path, ok); ok=false is
// the Python SystemExit branch. The installed-wheel staging (step 3's rename
// dance) is out of scope here — check() only needs a root that resolves to a
// flake.nix, and the run slice owns the staging (Stage 16).
func resolveRepoRoot(getenv func(string) string) (string, bool) {
	if env := getenv("YOLO_REPO_ROOT"); env != "" {
		if fileExists(filepath.Join(env, "flake.nix")) ||
			fileExists(filepath.Join(env, "src", "entrypoint", "__init__.py")) {
			if r, err := filepath.Abs(env); err == nil {
				return r, true
			}
			return env, true
		}
	}
	// Source-checkout detection: walk up from cwd looking for flake.nix (the Go
	// binary has no __file__; cwd is the workspace or repo during dev/CI).
	dir, err := os.Getwd()
	if err == nil {
		for {
			if fileExists(filepath.Join(dir, "flake.nix")) {
				if r, e := filepath.Abs(dir); e == nil {
					return r, true
				}
				return dir, true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

package check

import (
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// checkGOOS is the OS check judges setups for, from o.IsMacOS (a test states it).
func (o *Options) checkGOOS() string {
	if o.IsMacOS {
		return "darwin"
	}
	return "linux"
}

// checkSetup is the setup a launch from this workspace would select, through the launch's own
// call (config.SelectSetup; named-setups-plan.md slice 4a), so check reports the selection a
// launch makes: a removed YOLO_RUNTIME on the host is the selection's refusal, a YOLO_SETUP
// naming a Seatbelt setup on Linux is the setup's (setup.Buildable), and a removed config key
// still chooses its built-in when nothing names a setup.
func (o *Options) checkSetup(merged *jsonx.OrderedMap) (config.SetupChoice, error) {
	return config.SelectSetup(config.SetupInputs{
		Verb:      config.SetupForLaunch,
		Getenv:    o.Getenv,
		Merged:    merged,
		GOOS:      o.checkGOOS(),
		InSession: o.Getenv("YOLO_VERSION") != "",
	})
}

// runtimeForCheck resolves the effective runtime without exiting. Returns (runtime,
// errorMessage). A native (macos-user) setup short-circuits before any which/probe. Only returns
// container runtimes whose daemon is actually reachable.
func (o *Options) runtimeForCheck(config *jsonx.OrderedMap) (string, string) {
	rt, msg, _ := o.resolveRuntimeForCheck(config)
	return rt, msg
}

// resolveRuntimeForCheck is the launch's setup gate predicted (internal/cli/run/setupgate.go):
// the selected setup, refused as a launch refuses it, then its runtime installed and answering.
// unavailable is the one fact Merged Configuration needs to avoid counting a cause twice: true
// when the error is that the setup's CONTAINER runtime is not on PATH or not connected, which
// the Container Runtime section has already probed and graded. A refusal of the selection itself
// (a removed input, a setup this OS does not have, a host setup) is a config finding that
// section never makes, so it is not "unavailable".
func (o *Options) resolveRuntimeForCheck(merged *jsonx.OrderedMap) (rt, msg string, unavailable bool) {
	choice, err := o.checkSetup(merged)
	if err != nil {
		return "", err.Error(), false
	}
	if choice.Setup.Kind() == setup.KindHost {
		return "", "", false // `yolo host` runs it, with no runtime to grade
	}
	s := choice.Setup
	if err := setup.Buildable(s, o.checkGOOS(), nil); err != nil {
		return "", err.Error(), false
	}
	rt = s.Backend()
	if inStrSlice(paths.NativeRuntimes, rt) {
		return rt, "", false
	}
	if _, ok := o.LookPath(rt); !ok {
		return "", "Runtime '" + rt + "' for setup " + s.Name + " is not on PATH", true
	}
	// A `container` on PATH is not always Apple's: the name is generic. Asked, never assumed,
	// by the probe a launch's setup gate runs too, and a binary that is not it is a finding
	// with the launch's own next step, not a reason to grade another runtime.
	if err := setup.CheckAppleContainer(s, o.checkGOOS(), o.LookPath, o.appleContainerVersionOutput); err != nil {
		return "", err.Error(), false
	}
	if !o.runtimeIsConnectable(rt) {
		return "", "Runtime '" + rt + "' for setup " + s.Name + " is not connected", true
	}
	return rt, "", false
}

// configuredRuntimeName is the runtime of the setup a launch would select (containerSetup's for
// a host setup), "" when the selection refuses or names one this OS does not have (a Seatbelt
// setup on Linux): the backend a launch from here would run.
func (o *Options) configuredRuntimeName(merged *jsonx.OrderedMap) string {
	choice, err := o.checkSetup(merged)
	if err != nil {
		return ""
	}
	s := o.containerSetup(choice.Setup)
	if setup.Buildable(s, o.checkGOOS(), nil) != nil {
		return ""
	}
	return s.Backend()
}

// containerSetup is the setup whose runtime check grades: s, except that a host setup (a
// workspace whose removed `confinement` key says "host", or a user default of host-self) is
// `yolo host`'s and needs no runtime, so the runtime graded is the OS default's, which a launch
// from here names with --setup. check reports a host setup's own facts (its packages inert by
// design) in the sections that read the notch, never as a runtime refusal.
func (o *Options) containerSetup(s setup.Setup) setup.Setup {
	if s.Kind() == setup.KindHost {
		return setup.Default(o.checkGOOS())
	}
	return s
}

// explicitRuntimeName is configuredRuntimeName when something NAMED the setup (--setup has no
// check spelling, so YOLO_SETUP, the user config's `setup`, or a removed key), "" for the OS
// default: the runtime whose being offline is a finding of its own.
func (o *Options) explicitRuntimeName(merged *jsonx.OrderedMap) string {
	if choice, err := o.checkSetup(merged); err != nil || choice.Source == setup.FromDefault {
		return ""
	}
	return o.configuredRuntimeName(merged)
}

// appleContainerVersionOutput is what `container --version` prints, for setup.CheckAppleContainer,
// and whether it ran. It runs the name, as runtimeIsConnectable does, so both ask the binary a
// launch would run.
func (o *Options) appleContainerVersionOutput(string) (string, bool) {
	res := o.Exec([]string{"container", "--version"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return "", false
	}
	return res.Stdout + res.Stderr, true
}

// runtimeIsConnectable reports whether the daemon answers. Podman asks podmanGate, the
// launch's own probe, once per check: the readiness gate on Linux, the patient one-shot on
// macOS (podmanready.go). Apple Container keeps a one-shot probe.
func (o *Options) runtimeIsConnectable(rt string) bool {
	if o.asksPodmanGate(rt) {
		return o.podmanGate().Outcome == runtime.PodmanReady
	}
	res := o.Exec([]string{"container", "system", "status"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return false
	}
	return res.RC == 0 && strings.Contains(strings.ToLower(res.Stdout), "running")
}

// detectRuntimeForListing returns the first SUPPORTED
// runtime on PATH, or "".
func (o *Options) detectRuntimeForListing() string {
	for _, rt := range paths.SupportedRuntimes {
		if _, ok := o.LookPath(rt); ok {
			return rt
		}
	}
	return ""
}

// detectRuntime is the runtime of the setup a launch would select with no workspace config,
// else "podman".
func (o *Options) detectRuntime() string {
	if v := o.configuredRuntimeName(nil); v != "" {
		return v
	}
	return "podman"
}

// listRunningJailNames returns (names, errorMessage).
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

// getContainerWorkspace consults the tracking file first
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

// parseAppleInspectWorkspace scans data["config"]["env"] for YOLO_HOST_DIR=.
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

// checkContainerStuck returns a reason string if
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

// podmanMachineMemory returns (name, memMB, ok).
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
		// A missing/absent Resources yields no Memory, so this is not a valid
		// reading.
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

// configRuntime is the runtime the config NAMES, before any platform question: its `runtime`
// key, else the guest notch's backend (config.GuestRuntime) when `confinement` is "guest", else
// "". The guest half is platform-blind on purpose: its one reader that acts on a native answer,
// sectionContainerRuntime's early read, gates that answer on o.IsMacOS itself, so a macOS guest
// skips the container probes it does not need (env-manager plan EMP-D1) and a Linux guest is
// probed as before. Readers that need the platform answered use configuredRuntimeName.
func configRuntime(merged *jsonx.OrderedMap) string {
	if rt := runtimeKey(merged); rt != "" {
		return rt
	}
	if config.ResolveConfinement(merged) == config.ConfinementGuest {
		return config.GuestRuntime
	}
	return ""
}

// runtimeKey is the config's `runtime` value alone (config.RuntimeKey).
func runtimeKey(merged *jsonx.OrderedMap) string { return config.RuntimeKey(merged) }

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

// resolveRepoRoot delegates to the single shared resolver (internal/reporoot) so
// check and run use the same matched source. Returns (resolution, ok); on false,
// resolution.Error carries the resolver's actionable refusal for the finding.
func resolveRepoRoot(getenv func(string) string) (reporoot.Resolution, bool) {
	return reporoot.Resolve(getenv)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

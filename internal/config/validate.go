package config

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// LoopholeInfo is the subset of a discovered loophole that
// validateLoopholeOverride consults. HasHostDaemon is true when the loophole
// declares a host daemon.
type LoopholeInfo struct {
	Name          string
	HasHostDaemon bool
	// Settings are the loophole's manifest-declared config keys, in declaration
	// order — what makes `loopholes.<name>.settings` checkable rather than an
	// opaque map (docs/design/pack-config-keys.md).
	//
	// The DECLARATIONS travel rather than a pre-digested answer, because this
	// validator asks three different questions of them (does the key exist, does
	// the value have the declared type, may THIS file supply it) and a boolean per
	// question would be three chances for the resolver and the validator to
	// disagree about one manifest.
	//
	// An EMPTY list on a known loophole is meaningful and is not "unknown": it says
	// the loophole owns no config keys, so every supplied key is a typo. That is
	// only sound because OQ-K1 ruled declarations AUTHORITATIVE — launch resolves
	// packs from the local store with no network, and a pack that cannot be
	// resolved is already a fatal launch error, so there is no launch in which a
	// configured pack's declarations are missing and the jail starts anyway. The
	// "the loophole is not known at all" case (info == nil) is the one that stays
	// unvalidated, and it already has its own warning.
	Settings []loopholedecl.Setting
}

// LoopholeResolver supplies the file-backed loophole set (including disabled
// ones) for validation. It is injected; a nil resolver means "no known
// loopholes" (discovery degraded to empty).
//
// Known returns the map of name->info and a boolean that is false when
// discovery failed. A false ok on a truly-empty machine and a false ok on a
// discovery error are indistinguishable to ValidateConfig — both yield the
// empty known set — so callers may simply return (nil, true) for "empty" or
// (nil, false) for "discovery errored"; both behave identically downstream.
type LoopholeResolver interface {
	Known() (map[string]LoopholeInfo, bool)
}

// ValidateConfig returns (errors, warnings) in a fixed append order (a frozen
// contract — the order must not drift). workspace is used for mount-path
// existence checks (config.mounts). resolver supplies known loopholes
// (nil => none).
func ValidateConfig(config *jsonx.OrderedMap, workspace string, resolver LoopholeResolver) (errors []string, warnings []string) {
	if workspace == "" {
		workspace = cwd()
	}
	errs := &[]string{}
	warns := &[]string{}

	reportUnknownKeys(config, knownTopLevelConfigKeys, "config", errs)

	validateRuntime(config, errs)
	validateConfinement(config, errs)
	validateRepoPath(config, errs, warns)
	validateAgentsRetired(config, errs, warns)
	validatePackages(config, errs)
	validateMounts(config, workspace, errs, warns)
	validateWorkspaceReadonly(config, errs)
	validatePerSidePaths(config, errs)
	validateLoopholes(config, workspace, resolver, errs, warns)
	validateJournalRetired(config, errs, warns)
	validateKVM(config, errs)
	validateEphemeralStorage(config, errs)
	validateNetwork(config, errs, warns)
	validateSecurity(config, errs)
	validateHostProcessesRetired(config, errs, warns)
	validateMiseTools(config, errs)
	validateLSPServers(config, errs)
	validateMCPPresets(config, errs)
	validateMCPServers(config, errs)
	validateProviders(config, errs, warns)
	validateAgentProfilesRetired(config, errs, warns)
	validatePackProfiles(config, errs)
	validateRequiredCapabilities(config, errs)
	validateDevices(config, errs, warns)
	validateGPU(config, errs, warns)
	validateResources(config, errs)
	validateIncludeIfFound(config, errs)
	validateAgentsMdExtra(config, errs)
	validateEnvSources(config, errs)
	validateCacheRelocations(config, workspace, errs, warns)
	validateWritableHomeDirs(config, errs)
	validateHostFiles(config, workspace, errs)
	validateHostWrappers(config, workspace, errs)
	validatePacks(workspace, errs)

	errors = *errs
	warnings = *warns
	if errors == nil {
		errors = []string{}
	}
	if warnings == nil {
		warnings = []string{}
	}
	return errors, warnings
}

func add(list *[]string, s string) { *list = append(*list, s) }

// reportUnknownKeys iterates the mapping's keys in sorted order and appends
// "<path>.<key>: unknown key" for each not in allowed.
func reportUnknownKeys(m *jsonx.OrderedMap, allowed map[string]struct{}, path string, errs *[]string) {
	keys := append([]string(nil), m.Keys()...)
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := allowed[key]; !ok {
			add(errs, path+"."+key+": unknown key")
		}
	}
}

func validateRuntime(config *jsonx.OrderedMap, errs *[]string) {
	runtime, present := config.Get("runtime")
	if !present {
		return
	}
	if strEq(runtime, "docker") {
		add(errs, "config.runtime: 'docker' is no longer supported — "+
			"use 'podman' (Linux) or 'container' (macOS Apple Container)")
		return
	}
	if runtime != nil && !inStrList(paths.AllRuntimes, runtime) {
		add(errs, "config.runtime: expected 'podman', 'container', or 'macos-user'")
	}
}

// validateRepoPath handles the RETIRED repo_path key. It is still tolerated (it
// stays in knownTopLevelConfigKeys) so an existing config does not hard-error on
// upgrade, but it is no longer read by the resolver (internal/reporoot.Resolve,
// retired 2026-07-23) — so its presence earns a deprecation WARNING telling the
// user to drop it. A non-string value is still a type error.
func validateRepoPath(config *jsonx.OrderedMap, errs, warns *[]string) {
	repoPath, present := config.Get("repo_path")
	if !present || repoPath == nil {
		return
	}
	if _, ok := asStr(repoPath); !ok {
		add(errs, "config.repo_path: expected a string path")
		return
	}
	add(warns, "config.repo_path: ignored — this key was retired. yolo now finds "+
		"the repo from the flake bundle its install shipped (or YOLO_REPO_ROOT). Remove it.")
}

// validateAgentsRetired reports the DELETED `agents` key.
//
// The key is gone, not renamed: config now carries ONE list of `packs`, a pack
// that installs an agent is just a pack, and nothing in the pack machinery knows
// what an agent is. There is no DefaultAgents behind it any more either, so a
// config still naming agents would not merely be ignored — it would describe a
// selection yolo can no longer make.
//
// `agents` STAYS in knownTopLevelConfigKeys so this is the only message the key earns:
// dropping it from the set would add a generic "unknown key" alongside, reporting one
// mistake twice. This is the same treatment `docker` gets in validateRuntime and `env`
// gets in validateEnvSources — a bare "unknown key" reads like a typo and sends people
// hunting for the correct spelling of a key that no longer exists. Say it was removed,
// and say what replaced it.
//
// Everything that existed only to serve the key went with it: validateAgentsScope
// (the workspace-cannot-widen-the-user-set guard, which protected the credential
// boundary `agents` opened by being an override-list key) and validAgentSet.
//
// ERROR on the host, WARNING inside a jail, and the asymmetry is the whole point.
// A hard error is right on the host: the user is looking at the file they typed the key
// into, and silently ignoring it would mean they asked for claude, got nothing, and had
// nowhere to read why (with zero agents no briefing file is written). Inside a jail the
// config is NOT user-authored — LoadConfig prefers the host-generated, gitignored
// <workspace>/.yolo/config-assembled.json, falling back to the host user config mounted
// read-only. Erroring there refuses every nested launch over a key the in-jail user
// cannot fix at its source, and it made `yolo check` DISAGREE with launch: check merges
// the user and workspace files directly and never reads the snapshot, so it called the
// very config that just refused to launch "semantically valid" — while the error text
// told the user to run `yolo check`. Warning in-jail makes the two agree again and
// still reports the key. validateCacheRelocations carves out the same way, for the same
// snapshot reason.
func validateAgentsRetired(config *jsonx.OrderedMap, errs, warns *[]string) {
	if _, present := config.Get("agents"); !present {
		return
	}
	msg := "config.agents: REMOVED — which agents a jail gets is no longer a " +
		"config key of its own. An agent arrives as a pack, so name the pack that " +
		"installs it in `packs` instead. See `yolo config-ref` for the `packs` key " +
		"and `yolo pack --help` for the pack tooling."
	if inJail() {
		add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
			"so remove the key from the HOST config.)")
		return
	}
	add(errs, msg)
}

func validatePackages(config *jsonx.OrderedMap, errs *[]string) {
	packagesV, present := config.Get("packages")
	if !present || packagesV == nil {
		return
	}
	packages, ok := asList(packagesV)
	if !ok {
		add(errs, "config.packages: expected a list")
		return
	}
	for idx, pkgV := range packages {
		path := fmt.Sprintf("config.packages[%d]", idx)
		if s, ok := asStr(pkgV); ok {
			if !packageNameRe.MatchString(s) {
				add(errs, fmt.Sprintf("%s: invalid package name %s; "+
					"expected '<name>' or '<name>.<output>' "+
					"(letters, digits, '_' and '-' only; at most one dot)",
					path, pytext.Repr(s)))
			}
			continue
		}
		pkg, ok := asMap(pkgV)
		if !ok {
			add(errs, path+": expected a string or object")
			continue
		}
		reportUnknownKeys(pkg, knownPackageKeys, path, errs)
		nameV, _ := pkg.Get("name")
		if name, ok := asStr(nameV); !ok {
			add(errs, path+".name: expected a string")
		} else if strings.Contains(name, ".") {
			add(errs, path+".name: dotted output shorthand ('gtk4.dev') is "+
				"string-only; use the 'outputs' field on the object form")
		}
		outputsV, hasOutputs := pkg.Get("outputs")
		if hasOutputs {
			outputs, ok := asList(outputsV)
			allStr := ok
			if ok {
				for _, o := range outputs {
					if _, ok := asStr(o); !ok {
						allStr = false
						break
					}
				}
			}
			if !allStr {
				add(errs, path+`.outputs: expected a list of strings (e.g. ["out", "dev"])`)
			} else {
				for oIdx, o := range outputs {
					out, _ := asStr(o)
					if !packageOutputRe.MatchString(out) {
						add(errs, fmt.Sprintf("%s.outputs[%d]: invalid output name "+
							"%s (common values: out, dev, bin, lib, man, doc)",
							path, oIdx, pytext.Repr(out)))
					}
				}
			}
		}
		_, hasNixpkgs := pkg.Get("nixpkgs")
		hasVersionOverride := false
		for _, k := range []string{"version", "url", "hash"} {
			if _, ok := pkg.Get(k); ok {
				hasVersionOverride = true
				break
			}
		}
		if hasNixpkgs {
			nixpkgsV, _ := pkg.Get("nixpkgs")
			if _, ok := asStr(nixpkgsV); !ok {
				add(errs, path+".nixpkgs: expected a string")
			}
			if hasVersionOverride {
				add(errs, path+": use either nixpkgs pinning or version/url/hash overrides, not both")
			}
		} else if hasVersionOverride {
			for _, k := range []string{"version", "url", "hash"} {
				kv, _ := pkg.Get(k)
				if _, ok := asStr(kv); !ok {
					add(errs, path+"."+k+": expected a string")
				}
			}
		} else if !hasOutputs {
			add(errs, path+": object packages must use 'nixpkgs', "+
				"'version'+'url'+'hash', or 'outputs'")
		}
	}
}

func validateMounts(config *jsonx.OrderedMap, workspace string, errs, warns *[]string) {
	mountsV, present := config.Get("mounts")
	if !present || mountsV == nil {
		return
	}
	mounts, ok := asList(mountsV)
	if !ok {
		add(errs, "config.mounts: expected a list")
		return
	}
	for idx, mountV := range mounts {
		path := fmt.Sprintf("config.mounts[%d]", idx)
		mount, ok := asStr(mountV)
		if !ok {
			add(errs, path+": expected a string")
			continue
		}
		colonIdx := strings.LastIndex(mount, ":")
		hostPath := mount
		if colonIdx > 0 && colonIdx+1 < len(mount) && mount[colonIdx+1] == '/' {
			hostPath = mount[:colonIdx]
			containerPath := mount[colonIdx+1:]
			if !strings.HasPrefix(containerPath, "/") {
				add(errs, path+": container mount path must be absolute")
			}
		}
		if hostPath == "" {
			add(errs, path+": host mount path cannot be empty")
			continue
		}
		resolvedHost := expandAndResolve(hostPath)
		if !pathExists(resolvedHost) {
			add(warns, fmt.Sprintf("%s: host path does not exist and will be skipped: %s",
				path, resolvedHost))
		}
	}
}

func validateWorkspaceReadonly(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("workspace_readonly")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.workspace_readonly: expected a list of strings")
		return
	}
	for idx, entryV := range list {
		path := fmt.Sprintf("config.workspace_readonly[%d]", idx)
		entry, ok := asStr(entryV)
		if !ok {
			add(errs, path+": expected a string")
		} else if strings.HasPrefix(entry, "/") {
			add(errs, path+": must be a relative path, not absolute")
		} else if containsDotDot(entry) {
			add(errs, path+": must not contain '..' components")
		}
	}
}

func validatePerSidePaths(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("per_side_paths")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.per_side_paths: expected a list of strings")
		return
	}
	for idx, entryV := range list {
		path := fmt.Sprintf("config.per_side_paths[%d]", idx)
		entry, ok := asStr(entryV)
		if !ok {
			add(errs, path+": expected a string")
		} else if entry == "" || entry == "." {
			add(errs, path+": must name a workspace sub-path")
		} else if strings.HasPrefix(entry, "/") {
			add(errs, path+": must be a relative path, not absolute")
		} else if containsDotDot(entry) {
			add(errs, path+": must not contain '..' components")
		}
	}
}

// validateJournalRetired reports the DELETED top-level `journal` key.
//
// It was the SECOND of exactly two loopholes core's own config schema named by hand
// (docs/design/loophole-activation.md §1.4), and with `host_processes` already gone
// this is the one whose removal makes the sprint mean something: core's schema now
// names no loophole at all. The bridge ships as the official `journal` pack, and its
// mode is that loophole's own declared setting.
//
// A REFUSAL, NOT A WARNING, AND NOT SILENCE — the same three-way choice
// validateHostProcessesRetired describes, landing the same way for a sharper reason.
// This key did not merely configure a daemon: it TURNED ONE ON. A config that still
// says `"journal": "full"` and gets nothing has been silently denied a host
// capability it asked for, and "my logs stopped working" is not a symptom that leads
// anyone back to a key that was quietly ignored.
//
// THE MESSAGE HAS TO CARRY THREE THINGS, because migrating the value alone leaves
// `yolo-journalctl` just as broken: select the pack, enable the loophole, and — only
// for the `full` case — write the setting. It also has to say WHERE: `full` is
// declared `scope: "user"`, so writing it in a workspace `yolo-jail.jsonc` is itself
// refused. That scope IS the ruling (OQ-K4's "security half"), because
// `"journal": "full"` was settable from an agent-editable file with no scope rule of
// any kind.
//
// TYPE CHECKS WENT WITH THE KEY (the off/user/full enum and the bool alias). Telling
// someone their removed key has the wrong shape is two contradictory instructions
// about one line.
//
// ERROR ON THE HOST, WARNING INSIDE A JAIL, for the reason validateAgentsRetired
// states at length: in-jail the config is the HOST-GENERATED snapshot, so erroring
// there refuses every nested launch over a key the in-jail user cannot fix at its
// source, and it would make `yolo check` disagree with the launch. inherit.go stops
// emitting the key, so the downgrade covers exactly one population: a jail whose
// snapshot was written by a launcher older than this change.
func validateJournalRetired(config *jsonx.OrderedMap, errs, warns *[]string) {
	if _, present := config.Get("journal"); !present {
		return
	}
	msg := "config.journal: REMOVED — this top-level key was retired when the journal " +
		"bridge became a pack-shipped loophole, and yolo's config schema no longer names " +
		"a loophole. The bridge is now selected and switched on like anything else: " +
		`"packs": ["journal"] plus "loopholes": {"journal": {"enabled": true}}, which is ` +
		`what "journal": "user" (and the bare true) used to mean. The old "full" is one ` +
		`more key — "loopholes": {"journal": {"settings": {"full": true}}} — and it is ` +
		"USER-CONFIG-ONLY on purpose: reading the whole host journal used to be settable " +
		"from a workspace file the agent inside the jail can rewrite, and now it is not."
	if inJail() {
		add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
			"so remove the key from the HOST config.)")
		return
	}
	add(errs, msg)
}

func validateKVM(config *jsonx.OrderedMap, errs *[]string) {
	kvm, present := config.Get("kvm")
	if !present || kvm == nil {
		return
	}
	if !isBool(kvm) {
		add(errs, "config.kvm: expected a boolean (got "+pyReprValue(kvm)+")")
	}
}

// validateHostWrappers shape-checks the `host_wrappers` opt-in.
//
// It is a plain boolean by deliberate design, not a list of agents to wrap: which
// programs get a wrapper is not a user choice, it is every program a selected pack
// installs (host-agent-environment.md OQ-5). The one decision a user makes is whether the
// wrap directory exists and goes on their PATH at all, and that is what this key is.
func validateHostWrappers(config *jsonx.OrderedMap, workspace string, errs *[]string) {
	v, present := config.Get(hostWrappersKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key here proves
		// the workspace config has none either — no re-read needed.
		return
	}
	if v != nil && !isBool(v) {
		add(errs, "config."+hostWrappersKey+": expected a boolean (got "+pyReprValue(v)+")")
	}

	// Scope: the key is READ from the user config directly (HostWrappersEnabled), so a
	// workspace value is already inert. Say so rather than letting it look like it worked
	// — a silent no-op on a security-relevant key is exactly the failure mode this whole
	// construction exists to avoid. Warnings from the re-read are discarded: this file was
	// already loaded, and any parse problem already reported, by whoever produced the
	// merged config we were handed.
	wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(hostWrappersKey); atWorkspace && wsValue != nil {
		add(errs, "config."+hostWrappersKey+": user-scope only — it puts generated "+
			"executables on your PATH, so it is read from "+paths.UserConfigPath()+
			" and a workspace value has no effect. Move it there, or remove it.")
	}
}

func validateEphemeralStorage(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("ephemeral_storage")
	if !present || v == nil {
		return
	}
	s, ok := asStr(v)
	if !ok || !inStrSlice(ephemeralStorageModes, s) {
		add(errs, fmt.Sprintf("config.ephemeral_storage: expected one of %s (got %s)",
			pyListRepr(ephemeralStorageModes), pyReprValue(v)))
	}
}

func validateNetwork(config *jsonx.OrderedMap, errs, warns *[]string) {
	v, present := config.Get("network")
	if !present || v == nil {
		return
	}
	network, ok := asMap(v)
	if !ok {
		add(errs, "config.network: expected an object")
		return
	}
	reportUnknownKeys(network, knownNetworkKeys, "config.network", errs)
	mode, _ := network.Get("mode")
	if mode != nil && !strEq(mode, "bridge") && !strEq(mode, "host") {
		add(errs, "config.network.mode: expected 'bridge' or 'host'")
	}
	ports, portsPresent := network.Get("ports")
	if portsPresent && ports != nil {
		if pl, ok := asList(ports); ok {
			for idx, port := range pl {
				validatePublishPort(port, fmt.Sprintf("config.network.ports[%d]", idx), errs)
			}
		} else {
			add(errs, "config.network.ports: expected a list")
		}
	}
	fhp, fhpPresent := network.Get("forward_host_ports")
	if fhpPresent && fhp != nil {
		if fl, ok := asList(fhp); ok {
			for idx, port := range fl {
				validateForwardHostPort(port, fmt.Sprintf("config.network.forward_host_ports[%d]", idx), errs)
			}
		} else {
			add(errs, "config.network.forward_host_ports: expected a list")
		}
	}
	if strEq(mode, "host") {
		if pv, ok := network.Get("ports"); ok && truthy(pv) {
			add(warns, "config.network.ports: ignored when network.mode is 'host'")
		}
		if fv, ok := network.Get("forward_host_ports"); ok && truthy(fv) {
			add(warns, "config.network.forward_host_ports: ignored when network.mode is 'host'")
		}
	}
}

func validatePortNumber(value any, path string, errs *[]string) {
	port, ok := pyInt(value)
	if !ok {
		add(errs, path+": expected an integer port number")
		return
	}
	if port < 1 || port > 65535 {
		add(errs, path+": port must be between 1 and 65535")
	}
}

func validatePublishPort(value any, path string, errs *[]string) {
	s, ok := asStr(value)
	if !ok {
		add(errs, path+": expected a '<host>:<jail>' string like '8000:8000'")
		return
	}
	base := s
	if strings.Contains(base, "/") {
		i := strings.LastIndex(base, "/")
		protocol := base[i+1:]
		base = base[:i]
		if protocol != "tcp" && protocol != "udp" {
			add(errs, path+": protocol must be tcp or udp")
		}
	}
	parts := strings.Split(base, ":")
	var hostPort, containerPort string
	if len(parts) == 2 {
		hostPort, containerPort = parts[0], parts[1]
	} else if len(parts) == 3 {
		hostPort, containerPort = parts[1], parts[2]
	} else {
		// Host side FIRST — this is handed to podman's -p verbatim. The reverse of
		// network.forward_host_ports; see the direction table in `yolo config-ref`.
		add(errs, path+": expected '<host>:<jail>' or '<ip>:<host>:<jail>' "+
			"(host side FIRST — the reverse of network.forward_host_ports)")
		return
	}
	validatePortNumber(hostPort, path+".host", errs)
	validatePortNumber(containerPort, path+".container", errs)
}

func validateForwardHostPort(value any, path string, errs *[]string) {
	if isBool(value) {
		// A bool counts as an integer port here.
		validatePortNumber(value, path, errs)
		return
	}
	if _, ok := jsonx.AsInt(value); ok {
		validatePortNumber(value, path, errs)
		return
	}
	s, ok := asStr(value)
	if !ok {
		add(errs, path+": expected an int or string like '8080:9090'")
		return
	}
	parts := strings.Split(s, ":")
	if len(parts) == 1 {
		validatePortNumber(parts[0], path, errs)
		return
	}
	if len(parts) == 2 {
		validatePortNumber(parts[0], path+".jail", errs)
		validatePortNumber(parts[1], path+".host", errs)
		return
	}
	// "<jail>:<host>", never "<local>:<host>": "local" names whichever side the
	// reader is standing on, and this key's order is the REVERSE of
	// network.ports' "host:container". An error message is the worst place to
	// leave that ambiguous — see the direction table in `yolo config-ref`.
	add(errs, path+": expected '<port>' or '<jail>:<host>' "+
		"(jail side FIRST — the reverse of network.ports)")
}

func validateSecurity(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("security")
	if !present || v == nil {
		return
	}
	security, ok := asMap(v)
	if !ok {
		add(errs, "config.security: expected an object")
		return
	}
	reportUnknownKeys(security, knownSecurityKeys, "config.security", errs)
	bt, present := security.Get("blocked_tools")
	if !present || bt == nil {
		return
	}
	list, ok := asList(bt)
	if !ok {
		add(errs, "config.security.blocked_tools: expected a list")
		return
	}
	for idx, toolV := range list {
		path := fmt.Sprintf("config.security.blocked_tools[%d]", idx)
		if s, ok := asStr(toolV); ok {
			if !packdecl.ValidBinName(s) {
				// A blocked tool's name is a FILENAME in the generated block dir, and
				// blocked_tools reaches the entrypoint from the assembled config whose
				// workspace half is agent-editable — a name carrying ".." would write an
				// executable outside the anchor into the jail's persistent home.
				add(errs, path+": must be a bare tool name — no \"/\", \"..\", \":\" or absolute path ("+s+")")
			}
			continue
		}
		tool, ok := asMap(toolV)
		if !ok {
			add(errs, path+": expected a string or object")
			continue
		}
		reportUnknownKeys(tool, knownBlockedToolKeys, path, errs)
		if nameV, _ := tool.Get("name"); !isStr(nameV) {
			add(errs, path+".name: expected a string")
		} else if s, ok := asStr(nameV); ok && !packdecl.ValidBinName(s) {
			add(errs, path+".name: must be a bare tool name — no \"/\", \"..\", \":\" or absolute path ("+s+")")
		}
		for _, key := range []string{"message", "suggestion"} {
			if kv, ok := tool.Get(key); ok && !isStr(kv) {
				add(errs, path+"."+key+": expected a string")
			}
		}
		if bfV, ok := tool.Get("block_flags"); ok {
			if !isStrList(bfV) {
				add(errs, path+".block_flags: expected a list of strings")
			}
		}
	}
}

// validateHostProcessesRetired reports the DELETED top-level `host_processes` block.
//
// It was one of exactly two loopholes core's own config schema named by hand
// (docs/design/loophole-activation.md §1.4), and that is what made "convert the
// loophole to a pack" a separation in appearance only: the manifest would move out
// of core while core's schema went on naming it. The keys now live in the loophole's
// own manifest, in the official `host-processes` pack, under
// `loopholes.host-processes.settings` (docs/design/pack-config-keys.md).
//
// A REFUSAL, NOT A WARNING, AND NOT SILENCE. The previous step honored the key —
// folded it into the resolved settings at launch, with a warning naming the
// replacement — precisely so this step could delete it without stranding anybody.
// What must not happen is the third option: an ignored key. This block used to
// decide what a host daemon would reveal about the user's machine, so a config that
// still writes it and gets nothing has been silently DENIED a capability it asked
// for, in the one direction where silence reads as "it worked".
//
// The key STAYS in knownTopLevelConfigKeys so this is the only message it earns —
// the same treatment `agents` and `repo_path` get. A bare "unknown key" reads like a
// typo and sends people hunting for the correct spelling of a key that no longer
// exists.
//
// TYPE CHECKS ARE GONE with the key: there is nothing left to type-check for, and
// reporting `config.host_processes.visible: expected a list of strings` beside "this
// key was removed" would ask the user to fix the shape of something they must delete.
//
// ERROR ON THE HOST, WARNING INSIDE A JAIL, for the reason validateAgentsRetired
// states at length: in-jail the config is the HOST-GENERATED snapshot, so erroring
// there refuses every nested launch over a key the in-jail user cannot fix at its
// source — and it would make `yolo check` disagree with the launch. The snapshot no
// longer carries the key at all (inherit.go moved it to the retired block), so this
// downgrade covers exactly one population: a jail whose snapshot was written by a
// launcher older than this change.
func validateHostProcessesRetired(config *jsonx.OrderedMap, errs, warns *[]string) {
	if _, present := config.Get("host_processes"); !present {
		return
	}
	msg := "config.host_processes: REMOVED — this top-level key was retired when the " +
		"host-processes loophole became a pack, and yolo's config schema no longer names " +
		"a loophole. Write the values under the loophole's own name instead: " +
		`"loopholes": {"host-processes": {"settings": {"visible": [...], "fields": [...]}}}. ` +
		"NOTE THE SPELLING: the loophole is 'host-processes' (hyphen), not 'host_processes' " +
		"(underscore). Two more things changed with the move, and both need doing: the " +
		"loophole now ships in a pack, so `packs` must list \"host-processes\" and " +
		`"loopholes": {"host-processes": {"enabled": true}} must switch it on; and the ` +
		"allowlist is resolved ONCE at launch, so editing it needs a jail restart."
	if inJail() {
		add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
			"so remove the key from the HOST config.)")
		return
	}
	add(errs, msg)
}

func validateMiseTools(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("mise_tools")
	if !present || v == nil {
		return
	}
	mt, ok := asMap(v)
	if !ok {
		add(errs, "config.mise_tools: expected an object")
		return
	}
	for _, key := range mt.Keys() {
		value, _ := mt.Get(key)
		// Keys of a decoded JSON object are always strings, so only the value
		// (version) type is checked here.
		if _, ok := asStr(value); !ok {
			add(errs, "config.mise_tools."+key+": expected a version string")
		}
	}
}

func validateLSPServers(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("lsp_servers")
	if !present || v == nil {
		return
	}
	lsp, ok := asMap(v)
	if !ok {
		add(errs, "config.lsp_servers: expected an object")
		return
	}
	for _, name := range lsp.Keys() {
		cfgV, _ := lsp.Get(name)
		path := "config.lsp_servers." + name
		cfg, ok := asMap(cfgV)
		if !ok {
			add(errs, path+": expected an object")
			continue
		}
		reportUnknownKeys(cfg, knownLSPServerKeys, path, errs)
		if cmd, _ := cfg.Get("command"); !isStr(cmd) {
			add(errs, path+".command: expected a string")
		}
		if argsV, ok := cfg.Get("args"); ok {
			validateStringList(argsV, path+".args", errs)
		}
		feV, _ := cfg.Get("fileExtensions")
		fe, ok := asMap(feV)
		if !ok {
			add(errs, path+".fileExtensions: expected an object")
		} else {
			for _, ext := range fe.Keys() {
				lang, _ := fe.Get(ext)
				if !isStr(lang) {
					add(errs, path+".fileExtensions: keys and values must be strings")
				}
			}
		}
	}
}

func validateMCPPresets(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("mcp_presets")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.mcp_presets: expected an array of preset names")
		return
	}
	for idx, nameV := range list {
		name, ok := asStr(nameV)
		if !ok {
			add(errs, fmt.Sprintf("config.mcp_presets[%d]: expected a string", idx))
		} else if _, valid := validMCPPresets[name]; !valid {
			add(errs, fmt.Sprintf("config.mcp_presets[%d]: unknown preset '%s'. Valid presets: %s",
				idx, name, joinSorted(validMCPPresets)))
		}
	}
}

func validateMCPServers(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("mcp_servers")
	if !present || v == nil {
		return
	}
	servers, ok := asMap(v)
	if !ok {
		add(errs, "config.mcp_servers: expected an object")
		return
	}
	providesMap := map[string][]string{}
	for _, name := range servers.Keys() {
		cfgV, _ := servers.Get(name)
		path := "config.mcp_servers." + name
		if cfgV == nil {
			continue
		}
		cfg, ok := asMap(cfgV)
		if !ok {
			add(errs, path+": expected an object or null")
			continue
		}
		reportUnknownKeys(cfg, knownMCPServerKeys, path, errs)
		if cmd, _ := cfg.Get("command"); !isStr(cmd) {
			add(errs, path+".command: expected a string")
		}
		if argsV, ok := cfg.Get("args"); ok {
			validateStringList(argsV, path+".args", errs)
		}
		if envV, ok := cfg.Get("env"); ok {
			env, ok := asMap(envV)
			if !ok {
				add(errs, path+".env: expected an object")
			} else {
				for _, k := range env.Keys() {
					val, _ := env.Get(k)
					if !isStr(val) {
						add(errs, path+".env."+k+": expected string keys and values")
						break
					}
				}
			}
		}
		if reqV, ok := cfg.Get("requires_env"); ok {
			req, ok := asList(reqV)
			if !ok {
				add(errs, path+".requires_env: expected a list of env var names")
			} else {
				for rIdx, varV := range req {
					varName, ok := asStr(varV)
					if !ok || !envVarNameRe.MatchString(varName) {
						add(errs, fmt.Sprintf("%s.requires_env[%d]: invalid env var "+
							"name %s (must match [A-Za-z_][A-Za-z0-9_]*)",
							path, rIdx, pyReprValue(varV)))
					}
				}
			}
		}
		if provV, ok := cfg.Get("provides"); ok {
			if s, ok := asStr(provV); !ok || strings.TrimSpace(s) == "" {
				add(errs, path+".provides: expected a non-empty string")
			} else {
				providesMap[s] = append(providesMap[s], name)
			}
		}
	}
	var collCaps []string
	for capName := range providesMap {
		collCaps = append(collCaps, capName)
	}
	sort.Strings(collCaps)
	for _, capName := range collCaps {
		names := providesMap[capName]
		if len(names) > 1 {
			sort.Strings(names)
			add(errs, fmt.Sprintf("config.mcp_servers: multiple servers declare provides %q (%s). Ambiguous capability resolution.", capName, strings.Join(names, ", ")))
		}
	}
}

func validateProviders(config *jsonx.OrderedMap, errs, warns *[]string) {
	v, present := config.Get("providers")
	if !present || v == nil {
		return
	}
	providers, ok := asMap(v)
	if !ok {
		add(errs, "config.providers: expected an object")
		return
	}
	for _, name := range providers.Keys() {
		cfgV, _ := providers.Get(name)
		path := "config.providers." + name
		if cfgV == nil {
			continue // null disables or drops
		}
		cfg, ok := asMap(cfgV)
		if !ok {
			add(errs, path+": expected an object or null")
			continue
		}
		reportUnknownKeys(cfg, knownProviderKeys, path, errs)
		if s, ok := cfg.Get("env_shape"); ok && s != nil {
			validateProviderEnvShape(s, path, errs)
		}
		base, hasBase := cfg.Get("base_url")
		endpoints, hasEndpoints := cfg.Get("endpoints")
		// Closure rule 1 (zai OQ-Z6): the shorthand and the endpoint map are two ways to
		// say where a protocol points, and one provider carrying both is an ambiguity no
		// consumer could resolve. The refusal names `endpoints` because that is where a
		// URL belongs once more than one protocol is in play.
		if hasBase && base != nil && hasEndpoints && endpoints != nil {
			add(errs, path+": base_url and endpoints are both set — base_url is the "+
				"single-protocol shorthand and cannot be combined with it; move the URL "+
				"into endpoints, under the protocol it speaks (zai-plumbing.md §5)")
		}
		if u, ok := cfg.Get("base_url"); ok && u != nil {
			if s, isStrOk := asStr(u); !isStrOk {
				add(errs, path+".base_url: expected a string")
			} else if problem := providerURLProblem(s); problem != "" {
				add(errs, path+".base_url: "+problem)
			}
		}
		if hasEndpoints && endpoints != nil {
			validateProviderEndpoints(endpoints, path, errs)
		}
		if w, ok := cfg.Get("wire_api"); ok && w != nil {
			validateWireAPI(w, path+".wire_api", errs)
		}
		if r, ok := cfg.Get("region"); ok && r != nil {
			if !isStr(r) {
				add(errs, path+".region: expected a string")
			}
		}
		if a, ok := cfg.Get("api_key_env_name"); ok && a != nil {
			if s, ok := asStr(a); !ok || !envVarNameRe.MatchString(s) {
				add(errs, fmt.Sprintf("%s.api_key_env_name: invalid env var name %s (must match [A-Za-z_][A-Za-z0-9_]*)",
					path, pyReprValue(a)))
			}
		}
		if m, ok := cfg.Get("models"); ok && m != nil {
			models, ok := asMap(m)
			if !ok {
				add(errs, path+".models: expected an object")
			} else {
				for _, k := range models.Keys() {
					val, _ := models.Get(k)
					if !isStr(val) {
						add(errs, path+".models."+k+": expected a string model name")
					}
				}
			}
		}
		if c, ok := cfg.Get("capabilities"); ok && c != nil {
			validateStringList(c, path+".capabilities", errs)
		}
	}
}

// providerURLProblem returns what is wrong with a provider base_url, or "" when it is a
// usable address: it must parse as an http or https URL and carry no userinfo.
//
// The userinfo half is the credential rule (profiles-as-pack-variants.md §4.3):
// `https://user:tok@host/v1` is a credential in a git-tracked config file, and this rule
// is the check. A base_url routes an ADDRESS; the credential travels by NAME through
// api_key_env_name and is hydrated from env_sources. The scheme half is the same rule
// pointed the other way — `file://` or a bare host is a fact about the local machine, not
// about a service. packdecl's validateProviderEndpoints is this rule for the manifest
// layer, where the file ships to strangers; here it stays on the machine, so only the
// wording differs.
func providerURLProblem(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return err.Error()
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Sprintf("must be an http or https URL (%s)", u)
	}
	if parsed.User != nil {
		return fmt.Sprintf("must not carry userinfo — %q is a credential in a git-tracked "+
			"file; name an env var in api_key_env_name and let env_sources hydrate it", u)
	}
	return ""
}

// validateProviderEndpoints checks one provider's per-protocol endpoint map: every
// protocol names an endpoint object, whose base_url obeys providerURLProblem and whose
// wire_api is in the closed vocabulary.
//
// The PROTOCOL names stay open. Which protocols any agent speaks is decided by the
// resolution table (agentenv.agentProtocols) and by the derives in their own dialects, so
// a protocol nobody speaks yet is inert rather than invalid — closing the names here would
// make the config schema the one place a new protocol has to be registered, ahead of
// anything that could consume it. The VALUES are schema, and an unknown key inside an
// endpoint is refused for the same reason one at the provider level is: accepted one level
// down, it would be inert where the user meant it to work.
func validateProviderEndpoints(v any, path string, errs *[]string) {
	endpoints, ok := asMap(v)
	if !ok {
		add(errs, path+".endpoints: expected an object")
		return
	}
	for _, proto := range endpoints.Keys() {
		epPath := path + ".endpoints." + proto
		epV, _ := endpoints.Get(proto)
		if epV == nil {
			continue // null disables the protocol's endpoint
		}
		ep, ok := asMap(epV)
		if !ok {
			add(errs, epPath+": expected an object or null")
			continue
		}
		reportUnknownKeys(ep, knownEndpointKeys, epPath, errs)
		if u, ok := ep.Get("base_url"); ok && u != nil {
			if s, isStrOk := asStr(u); !isStrOk {
				add(errs, epPath+".base_url: expected a string")
			} else if problem := providerURLProblem(s); problem != "" {
				add(errs, epPath+".base_url: "+problem)
			}
		}
		if w, ok := ep.Get("wire_api"); ok && w != nil {
			validateWireAPI(w, epPath+".wire_api", errs)
		}
	}
}

// validateProviderEnvShape checks the env_shape a user wrote over a pack-shipped
// provider (profiles-as-pack-variants.md §14, OQ-15): the field is a service fact like
// endpoints and models, so the override is legal, but its values stay placeholders —
// never literals.
//
// The placeholder set is not restated here. packdecl.ValidateProviderEnvShape is the one
// vocabulary for both spellings of a provider, and this pass hands it the user's map
// verbatim, so a placeholder accepted in a manifest is exactly the placeholder accepted
// in config and a second copy of the set cannot drift away from the first. Only the
// SHAPE of the value is this file's business, checked the way every other provider key
// is ("expected an object", "expected a string"), so a mis-typed override is refused for
// its type before the placeholder check ever reads it.
func validateProviderEnvShape(v any, path string, errs *[]string) {
	shape, ok := asMap(v)
	if !ok {
		add(errs, path+".env_shape: expected an object")
		return
	}
	out := make(map[string]map[string]string)
	for _, proto := range shape.Keys() {
		protoPath := path + ".env_shape." + proto
		protoV, _ := shape.Get(proto)
		if protoV == nil {
			continue // null disables the protocol's shape
		}
		vars, ok := asMap(protoV)
		if !ok {
			add(errs, protoPath+": expected an object or null")
			continue
		}
		entry := make(map[string]string)
		for _, name := range vars.Keys() {
			val, _ := vars.Get(name)
			s, ok := val.(string)
			if !ok {
				add(errs, protoPath+"."+name+": expected a string")
				continue
			}
			entry[name] = s
		}
		out[proto] = entry
	}
	for _, problem := range packdecl.ValidateProviderEnvShape(path, out) {
		add(errs, problem)
	}
}

// validateWireAPI checks the wire protocol a provider speaks. A closed vocabulary rather
// than a free string because the value is the CANONICAL name the derives translate from
// (provider-table-fidelity.md §3.4 / OQ-PT1): a name outside the set translates to
// nothing, so it would reach every agent as no protocol at all — silently, from a jail
// that booted green. Rule 4, applied to a fixed slot.
//
// The set is not restated here. packdecl's KnownWireAPIs is the one vocabulary for both
// spellings of a provider — the entry a user writes and the entry a pack ships compose
// into the same table — so this pass asks for it rather than keeping a second copy that
// could drift away from the one the manifest layer enforces
// (packdecl.validateProviderEndpoints). That is the field's own contract:
// ProviderEndpoint.WireAPI's "the enum that tightens one tightens both".
func validateWireAPI(w any, path string, errs *[]string) {
	apis := packdecl.KnownWireAPIs()
	if !inStrList(apis, w) {
		add(errs, fmt.Sprintf("%s: expected one of %s (got %s)",
			path, pyListRepr(apis), pyReprValue(w)))
	}
}

// validateAgentProfilesRetired refuses the pre-rename spelling by name, the
// validateJournalRetired pattern: error on the host, warning in-jail (a snapshot
// written by an older launcher legitimately carries the old key).
func validateAgentProfilesRetired(config *jsonx.OrderedMap, errs, warns *[]string) {
	if _, present := config.Get("agent_profiles"); !present {
		return
	}
	msg := "config.agent_profiles: RENAMED — this key is now `pack_profiles`, because " +
		"the keys were always CLI names and core knows packs, not agents " +
		"(docs/design/profiles-as-pack-variants.md §3.3). Rename the key in place; " +
		"the values are unchanged."
	if inJail() {
		add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
			"so rename the key in the HOST config.)")
		return
	}
	add(errs, msg)
}

func validatePackProfiles(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("pack_profiles")
	if !present || v == nil {
		return
	}
	profiles, ok := asMap(v)
	if !ok {
		add(errs, "config.pack_profiles: expected an object")
		return
	}
	// The KEY is a CLI name — the binary a pack installs — and an unknown one is fatal
	// (§2.5, §8). Before this check {"cloude": "bedrock"} validated clean and silently
	// did nothing, which is the live hole the design documents: the values were checked
	// as strings while the thing they were keyed by was never checked at all.
	//
	// A null value REMOVES a profile and asserts nothing about its key, so an object
	// holding only nulls costs no pack resolution at all — the same leniency the
	// retired-key convention gives a key being deleted.
	keys := profiles.Keys()
	wantsNamespace := false
	for _, k := range keys {
		if val, _ := profiles.Get(k); val != nil {
			wantsNamespace = true
			break
		}
	}
	installed, namespaceKnown := []string(nil), false
	if wantsNamespace {
		// An unresolvable configured pack makes the namespace unknowable, and that pack
		// is refused on its own terms — louder, and first — by `yolo check`'s Packs
		// section and by the launch's staging. Reporting it here too would misdiagnose a
		// broken install as a typo'd profile key, so the NAMESPACE half steps aside; the
		// shape half below still runs, because a value that is not a string is a fact
		// about this config alone.
		if names, known := PackProfileCLINames(); known {
			installed, namespaceKnown = names, true
		}
	}
	for _, agent := range keys {
		profV, _ := profiles.Get(agent)
		path := "config.pack_profiles." + agent
		if profV == nil {
			continue
		}
		if !isStr(profV) {
			add(errs, path+": expected a string profile name")
			continue
		}
		if namespaceKnown && !containsStr(installed, agent) {
			add(errs, path+": "+unknownProfileCLIMessage(agent, installed))
		}
	}
}

// unknownProfileCLIMessage explains a pack_profiles key no resolvable pack answers to.
//
// It lists what IS installed, because the most likely cause is a typo in a tool name and
// the real list is the whole fix — the same reason unknownEmbeddedMessage lists pack
// names, and the same list: both name the CLIs yolo can actually launch.
func unknownProfileCLIMessage(key string, installed []string) string {
	have := "none"
	if len(installed) > 0 {
		have = strings.Join(installed, ", ")
	}
	return fmt.Sprintf("no pack installs a CLI named %q (installed: %s) — a pack_profiles "+
		"key selects a profile by the binary a pack installs, not by pack or agent name",
		key, have)
}

func validateRequiredCapabilities(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("required_capabilities")
	if !present || v == nil {
		return
	}
	validateStringList(v, "config.required_capabilities", errs)
}

func validateDevices(config *jsonx.OrderedMap, errs, warns *[]string) {
	v, present := config.Get("devices")
	if !present || v == nil {
		return
	}
	devices, ok := asList(v)
	if !ok {
		add(errs, "config.devices: expected a list")
		return
	}
	for idx, deviceV := range devices {
		path := fmt.Sprintf("config.devices[%d]", idx)
		if s, ok := asStr(deviceV); ok {
			if !pathExists(s) {
				add(warns, fmt.Sprintf("%s: device path does not exist and may be skipped: %s", path, s))
			}
			continue
		}
		device, ok := asMap(deviceV)
		if !ok {
			add(errs, path+": expected a string or object")
			continue
		}
		reportUnknownKeys(device, knownDeviceKeys, path, errs)
		_, hasUSB := device.Get("usb")
		_, hasCgroup := device.Get("cgroup_rule")
		if hasUSB == hasCgroup {
			add(errs, path+": expected exactly one of 'usb' or 'cgroup_rule'")
			continue
		}
		if hasUSB {
			usbV, _ := device.Get("usb")
			if usb, ok := asStr(usbV); !ok {
				add(errs, path+".usb: expected a string")
			} else if !usbIDRe.MatchString(usb) {
				add(errs, path+".usb: expected vendor:product hex format like '0bda:2838'")
			}
			if descV, ok := device.Get("description"); ok && !isStr(descV) {
				add(errs, path+".description: expected a string")
			}
		}
		if hasCgroup {
			cgV, _ := device.Get("cgroup_rule")
			if !isStr(cgV) {
				add(errs, path+".cgroup_rule: expected a string")
			}
		}
	}
}

func validateGPU(config *jsonx.OrderedMap, errs, warns *[]string) {
	v, present := config.Get("gpu")
	if !present || v == nil {
		return
	}
	gpu, ok := asMap(v)
	if !ok {
		add(errs, "config.gpu: expected an object")
		return
	}
	reportUnknownKeys(gpu, knownGPUKeys, "config.gpu", errs)
	if enabled, ok := gpu.Get("enabled"); ok && enabled != nil && !isBool(enabled) {
		add(errs, "config.gpu.enabled: expected a boolean")
	}
	vendorV, _ := gpu.Get("vendor")
	if vendorV != nil && !strEq(vendorV, "nvidia") && !strEq(vendorV, "amd") {
		add(errs, "config.gpu.vendor: expected 'nvidia' or 'amd'")
	}
	isAMD := strEq(vendorV, "amd")

	if dv, ok := gpu.Get("devices"); ok && dv != nil && !isStr(dv) {
		add(errs, "config.gpu.devices: expected a string ('all', '0', or '0,1')")
	}

	modeV, _ := gpu.Get("mode")
	if modeV != nil {
		if !isAMD {
			add(errs, "config.gpu.mode: only valid when vendor='amd'")
		} else if !strEq(modeV, "devices") && !strEq(modeV, "cdi") {
			add(errs, "config.gpu.mode: expected 'devices' or 'cdi'")
		}
	}

	capV, _ := gpu.Get("capabilities")
	if capV != nil {
		if isAMD {
			add(errs, "config.gpu.capabilities: not supported for vendor='amd' "+
				"(ROCm has no driver-capabilities concept)")
		} else if capsStr, ok := asStr(capV); !ok {
			add(errs, "config.gpu.capabilities: expected a string (e.g. 'compute,utility')")
		} else {
			validCaps := set("compute", "utility", "graphics", "video", "display", "compat32")
			for _, cap := range strings.Split(capsStr, ",") {
				cap = strings.TrimSpace(cap)
				if cap != "" {
					if _, ok := validCaps[cap]; !ok {
						add(errs, fmt.Sprintf("config.gpu.capabilities: unknown capability '%s'. Valid: %s",
							cap, joinSorted(validCaps)))
					}
				}
			}
		}
	}

	gfxV, _ := gpu.Get("hsa_override_gfx_version")
	if gfxV != nil {
		if !isAMD {
			add(errs, "config.gpu.hsa_override_gfx_version: only valid when vendor='amd'")
		} else if !isStr(gfxV) {
			add(errs, "config.gpu.hsa_override_gfx_version: expected a string (e.g. '11.0.0')")
		}
	}

	if seccompV, ok := gpu.Get("seccomp_unconfined"); ok && seccompV != nil && !isBool(seccompV) {
		add(errs, "config.gpu.seccomp_unconfined: expected a boolean")
	}

	vaapiV, hasVaapi := gpu.Get("vaapi")
	if hasVaapi && vaapiV != nil {
		if !isBool(vaapiV) {
			add(errs, "config.gpu.vaapi: expected a boolean")
		} else if truthy(vaapiV) && !isAMD {
			add(errs, "config.gpu.vaapi: currently requires vendor='amd' "+
				"(mesa radeonsi is the only wired-up VA-API driver)")
		} else if truthy(vaapiV) && !truthy(getOr(gpu, "enabled", nil)) {
			add(warns, "config.gpu.vaapi: inert without gpu.enabled=true "+
				"(no devices are passed through)")
		}
	}
}

func validateResources(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("resources")
	if !present || v == nil {
		return
	}
	resources, ok := asMap(v)
	if !ok {
		add(errs, "config.resources: expected an object")
		return
	}
	reportUnknownKeys(resources, knownResourcesKeys, "config.resources", errs)
	memoryV, _ := resources.Get("memory")
	if memoryV != nil {
		if memory, ok := asStr(memoryV); !ok {
			add(errs, "config.resources.memory: expected a string (e.g. '8g', '512m')")
		} else if !memoryRe.MatchString(memory) {
			add(errs, "config.resources.memory: invalid format. "+
				"Use a number with optional suffix: b, k, m, g (e.g. '8g', '512m')")
		}
	}
	cpusV, _ := resources.Get("cpus")
	if cpusV != nil {
		if isBool(cpusV) {
			// A bool counts as an int: true(1)>0 ok, false(0)<=0 -> error.
			n := int64(0)
			if cpusV.(bool) {
				n = 1
			}
			if n <= 0 {
				add(errs, "config.resources.cpus: must be a positive number")
			}
		} else if n, ok := jsonx.AsInt(cpusV); ok {
			if n <= 0 {
				add(errs, "config.resources.cpus: must be a positive number")
			}
		} else if f, ok := cpusV.(float64); ok {
			if f <= 0 {
				add(errs, "config.resources.cpus: must be a positive number")
			}
		} else if s, ok := asStr(cpusV); ok {
			if val, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err != nil {
				add(errs, "config.resources.cpus: expected a number (e.g. 4, 2.5, '0.5')")
			} else if val <= 0 {
				add(errs, "config.resources.cpus: must be a positive number")
			}
		} else {
			add(errs, "config.resources.cpus: expected a number (e.g. 4, 2.5, '0.5')")
		}
	}
	pidsV, _ := resources.Get("pids_limit")
	if pidsV != nil {
		// A bool counts as an int. Non-int, or <=0 -> error.
		if isBool(pidsV) {
			n := int64(0)
			if pidsV.(bool) {
				n = 1
			}
			if n <= 0 {
				add(errs, "config.resources.pids_limit: expected a positive integer")
			}
		} else if n, ok := jsonx.AsInt(pidsV); ok {
			if n <= 0 {
				add(errs, "config.resources.pids_limit: expected a positive integer")
			}
		} else {
			add(errs, "config.resources.pids_limit: expected a positive integer")
		}
	}
}

func validateIncludeIfFound(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("include_if_found")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.include_if_found: expected a list of relative path strings")
		return
	}
	for idx, entryV := range list {
		path := fmt.Sprintf("config.include_if_found[%d]", idx)
		entry, ok := asStr(entryV)
		if !ok {
			add(errs, path+": expected a string")
		} else if entry == "" {
			add(errs, path+": empty string is not a valid path")
		} else if strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, "~") {
			add(errs, fmt.Sprintf("%s: must be a relative path (got %s); "+
				"absolute paths and '~' are not supported", path, pytext.Repr(entry)))
		}
	}
}

func validateAgentsMdExtra(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("agents_md_extra")
	if present && v != nil && !isStr(v) {
		add(errs, "config.agents_md_extra: expected a string of markdown")
	}
}

func validateEnvSources(config *jsonx.OrderedMap, errs *[]string) {
	if _, hasEnv := config.Get("env"); hasEnv {
		add(errs, "config.env: removed — rename to 'env_sources' (an ordered list where "+
			`strings are KEY=VALUE files and objects are inline {"KEY": "VALUE"} sets). `+
			"See `yolo config-ref`.")
	}
	v, present := config.Get("env_sources")
	if !present || v == nil {
		return
	}
	list, ok := asList(v)
	if !ok {
		add(errs, "config.env_sources: expected a list of strings (file paths) "+
			"or objects (inline env maps)")
		return
	}
	for idx, entryV := range list {
		path := fmt.Sprintf("config.env_sources[%d]", idx)
		if s, ok := asStr(entryV); ok {
			if s == "" {
				add(errs, path+": empty string is not a valid path")
			}
			continue
		}
		if entry, ok := asMap(entryV); ok {
			for _, key := range entry.Keys() {
				value, _ := entry.Get(key)
				// "" is a valid JSON key, so an empty key is rejected here.
				if key == "" {
					add(errs, path+": inline map keys must be non-empty strings")
				} else if !envVarNameRe.MatchString(key) {
					add(errs, path+"."+key+": invalid variable name "+
						"(must match [A-Za-z_][A-Za-z0-9_]*)")
				}
				// null is the REMOVAL spelling — `unset KEY` for a host launch
				// (host-agent-environment.md §6.1 step 3). It must be accepted here or
				// the one payload no config surface can express is unconfigurable: the
				// very config the feature requires would be a `yolo check` error.
				if value != nil && !isStr(value) {
					add(errs, path+"."+key+": expected a string value, or null to unset")
				}
			}
			continue
		}
		add(errs, fmt.Sprintf("%s: expected a string (file path) or object (inline map), got %s",
			path, typeName(entryV)))
	}
}

// validateCacheRelocations enforces the two cache_relocations rules: the key is
// user-scope only, and every entry is shape-valid.
//
// The scope rule exists because a relocation is a read-write host mount and a
// workspace config is agent-editable (see LoadCacheRelocations for the full
// threat model). LoadCacheRelocations already ignores workspace scope entirely,
// so this check is defense-in-depth: without it, a workspace-scoped key is a
// silent no-op that looks like a broken feature.
//
// ValidateConfig only ever receives the MERGED map (cli/run/preflight.go,
// cli/check/check.go; merged in LoadConfig), and the merge carries no
// provenance — so the only way to tell where the key came from is to re-read
// the workspace config. That is one extra file read on a cold path, and much
// cheaper than threading provenance through every caller of the merge.
//
// warns is unused on purpose: a misconfigured relocation is always an error.
// Downgrading any of these to a warning would let the run proceed with the
// cache silently un-relocated, which is the exact failure mode the feature
// exists to prevent.
func validateCacheRelocations(config *jsonx.OrderedMap, workspace string, errs, warns *[]string) {
	v, present := config.Get(cacheRelocationsKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key
		// here proves the workspace config has none either — no re-read needed.
		return
	}
	// Warnings from the re-read are discarded: this same file was already
	// loaded (and any parse problem already reported) by whoever produced the
	// merged config we were handed.
	if wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {}); err == nil && wsCfg != nil {
		if _, atWorkspace := wsCfg.Get(cacheRelocationsKey); atWorkspace {
			add(errs, "config."+cacheRelocationsKey+": user-scope only — move it to "+
				"~/.config/yolo-jail/config.jsonc (a workspace config is "+
				"agent-editable, so it cannot grant read-write host mounts)")
		}
	}
	if v == nil {
		return
	}
	// The target-parent check is skipped inside a jail. Unlike the loader, this
	// runs against the MERGED config, which in a jail is the host-written
	// assembled config (LoadConfig prefers <workspace>/.yolo/config-assembled.json) or
	// the host user config bind-mounted read-only — either way it carries the
	// host's cache_relocations, whose targets are host paths deliberately not
	// present in the jail's mount namespace. Stat'ing them here would turn a
	// perfectly valid host config into a fatal "parent directory of the target
	// does not exist" on every nested `yolo` run and every in-jail `yolo check`.
	// The shape, scope and duplicate rules still apply everywhere; only the
	// filesystem probe is host-only, and the host run has already done it for
	// real before writing the snapshot.
	_, problems := checkCacheRelocations(v, !inJail())
	for _, p := range problems {
		add(errs, p)
	}
}

// validateStringList checks that values is a list of strings.
func validateStringList(values any, path string, errs *[]string) {
	list, ok := asList(values)
	if !ok {
		add(errs, path+": expected a list")
		return
	}
	for idx, value := range list {
		if !isStr(value) {
			add(errs, fmt.Sprintf("%s[%d]: expected a string", path, idx))
		}
	}
}

package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
	// opaque map (docs/reference/pack-system.md).
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
	// Brokered is the loophole's `brokered` block, or nil. Its presence among the
	// selected packs' loopholes is what arms the mount fence (validateBrokerMountFence).
	Brokered *loopholedecl.Brokered
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
	return validateConfigWithEditContext(config, workspace, resolver, nil)
}

func validateConfigWithEditContext(config *jsonx.OrderedMap, workspace string, resolver LoopholeResolver, edit *editValidationContext) (errors []string, warnings []string) {
	readableInput := config
	config, projection := projectReadableB2ForValidation(config)
	if workspace == "" {
		workspace = cwd()
	}
	if edit != nil && resolver == nil {
		resolver = explicitPackLoopholeResolver{packs: edit.packs}
	}
	errs := &[]string{}
	warns := &[]string{}

	// convert_later is judged by validateConvertLater alone, so the unknown-key reporters read
	// the config without it.
	if edit == nil {
		validateNoInterruptedMigration(workspace, errs)
		reportUnknownKeys(withoutConvertLater(config), withReadableScopeKeys(knownTopLevelConfigKeys), "config", errs)
	} else {
		reportUnknownKeysWithReadableFG(withoutConvertLater(config), errs)
	}
	validateConvertLater(config, workspace, errs, edit)

	validateRuntime(config, errs)
	validateConfinement(config, errs)
	validateRepoPath(config, errs, warns)
	validateAgentsRetired(config, errs, warns)
	validatePackages(config, errs)
	validateMounts(config, workspace, errs, warns, edit)
	validateAllowMounts(config, errs)
	validateWorkspaceAllow(config, workspace, errs, edit)
	if edit == nil {
		validateRequestsUserScope(config, errs)
	}
	validateWorkspaceReadonly(config, errs)
	validatePerSidePaths(config, errs)
	validateLoopholes(config, workspace, resolver, errs, warns, edit)
	validateBrokerMountFence(config, workspace, resolver, errs, warns, edit)
	validateBrokered(config, workspace, resolver, errs, warns, edit)
	validateWorkspaceFile(workspace, errs, warns)
	validateJournalRetired(config, errs, warns)
	validateKVM(config, errs)
	validateEphemeralStorage(config, errs)
	validateMacosLogRetired(config, errs, warns)
	validateNetwork(config, errs, warns)
	for _, message := range projection.errors {
		add(errs, message)
	}
	validateSecurity(config, errs)
	validateHostProcessesRetired(config, errs, warns)
	validateMiseTools(config, errs)
	validateLSPServers(config, errs)
	validateMCPPresetsRetired(config, errs, warns)
	validateMCPServers(config, errs)
	validatePackMCPServers(errs, edit)
	if edit == nil {
		validateProviders(config, workspace, errs, warns)
	} else {
		validateProvidersForExplicit(config, readableInput, edit, errs, warns)
	}
	validateAgentProfilesRetired(config, errs, warns)
	validateUseProfilesRetired(config, errs, warns)
	validateProfile(config, errs, edit)
	if edit == nil {
		validateActiveProvider(config, workspace, errs)
	}
	if edit == nil {
		validateProfiles(workspace, errs)
		validateAdapters(workspace, errs)
	} else {
		validateProfilesWithScopes(workspace, edit.rawUser, edit.rawWorkspace, errs)
		validateAdaptersWithScopes(workspace, edit.rawUser, edit.rawWorkspace, errs)
	}
	validatePrune(config, errs)
	validateRequiredCapabilities(config, errs)
	validateDevices(config, errs, warns)
	validateGPU(config, errs, warns)
	validateResources(config, errs)
	validateIncludeIfFound(config, errs)
	validateAgentsMdExtra(config, errs)
	validateBriefingProvenance(config, errs)
	validateEnvSources(config, errs)
	// The unprojected input: the projection above drops a readable `env` table, and its
	// `files` list is what the scope rule reads in the TOML spelling.
	validateEnvSourceScope(readableInput, workspace, errs, edit)
	if projection.env {
		validateReadableEnvironment(readableInput, errs)
	}
	validateCacheRelocations(config, workspace, errs, warns, edit)
	if edit == nil {
		validateWritableHomeDirs(config, errs)
	} else {
		validateWritableHomeDirs(config, errs, edit.packs)
	}
	validateHostFiles(config, workspace, errs, edit)
	validateHostWrappers(config, workspace, errs, edit)
	validateShareHostFonts(config, workspace, errs, edit)
	validateLocalClones(config, workspace, errs, edit)
	validateHostApplyOnLaunch(config, workspace, errs, edit)
	validateHostManagement(config, workspace, errs, edit)
	validateHostImportExclusions(config, workspace, errs, edit)
	validateAgentUpdates(config, workspace, errs, edit)
	validateHostFloor(config, workspace, errs, edit)
	validateHostPath(config, workspace, errs, edit)
	validateProvisioners(config, workspace, errs, warns, edit)
	validatePerfLogging(config, workspace, errs, edit)
	validateMacosUserMode(config, workspace, errs, edit)
	validateMacosUserSharedTmp(config, workspace, errs, edit)
	validateOnRequest(config, workspace, errs, edit)
	validateUpdateCheck(config, workspace, errs, edit)
	validatePromotionTarget(config, workspace, errs, edit)
	validatePacks(workspace, errs, edit)
	validatePrograms(config, workspace, errs, edit)

	errors = *errs
	warnings = *warns
	if projection.changed {
		errors = projectReadableValidationMessages(errors, projection)
		warnings = projectReadableValidationMessages(warnings, projection)
	}
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

// invalidPackagePath is the refusal for a `packages` name that is not a nixpkgs
// attribute path (packageNameRe), saying what one is and how to find the right one.
func invalidPackagePath(s string) string {
	return fmt.Sprintf("invalid package name %s; expected a nixpkgs attribute path, "+
		"as `nix build nixpkgs#<path>` takes it: 'strace', an output like 'gtk4.dev', "+
		"or a collection member like 'rocmPackages.clr' — names of letters, digits, "+
		"'_' and '-', separated by single dots, with any other name quoted as Nix "+
		"quotes it ('nerd-fonts.\"m+\"'). `nix search nixpkgs <name>` finds the path.",
		pytext.Repr(s))
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
				add(errs, path+": "+invalidPackagePath(s))
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
		// The object's name is the same attribute path a string entry is, so every
		// string entry can be rewritten as {"name": <it>, "platforms": [...]}: the
		// spelling the macos-user launch refusal tells the user to write.
		if name, ok := asStr(nameV); !ok {
			add(errs, path+".name: expected a string")
		} else if !packageNameRe.MatchString(name) {
			add(errs, path+".name: "+invalidPackagePath(name))
		}
		// platforms: the entry applies only on these GOOS values. An UNKNOWN value is
		// an error rather than an entry that quietly never matches — a typo like
		// "macos" or "x86_64-linux" would otherwise silently drop the package on every
		// platform, which is the failure this whole item exists to stop (a declared
		// package vanishing without a word).
		platformsV, hasPlatforms := pkg.Get("platforms")
		if hasPlatforms {
			list, ok := asList(platformsV)
			if !ok {
				add(errs, path+`.platforms: expected a list of strings (e.g. ["linux"])`)
			} else if len(list) == 0 {
				add(errs, path+".platforms: must not be empty — an entry that applies "+
					"on no platform would be silently dropped everywhere; delete the "+
					"package instead")
			} else {
				for pIdx, pv := range list {
					pStr, ok := asStr(pv)
					if !ok {
						add(errs, fmt.Sprintf("%s.platforms[%d]: expected a string", path, pIdx))
						continue
					}
					if pStr != PlatformLinux && pStr != PlatformDarwin {
						add(errs, fmt.Sprintf("%s.platforms[%d]: unknown platform %s "+
							"(expected %q or %q — the Go GOOS spelling, not a nix system "+
							"double like \"x86_64-linux\")",
							path, pIdx, pytext.Repr(pStr), PlatformLinux, PlatformDarwin))
					}
				}
			}
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
		} else if !hasOutputs && !hasPlatforms {
			// `platforms` ALONE is a complete object form. The object exists to carry
			// something a bare string cannot, and a platform restriction is exactly
			// that — {"name": "strace", "platforms": ["linux"]} is the spelling the
			// macos-user skip error tells users to write, so it has to validate on its
			// own. Found 2026-09-04 by running that error's own advice.
			add(errs, path+": object packages must use 'nixpkgs', "+
				"'version'+'url'+'hash', 'outputs', or 'platforms'")
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
// (docs/reference/loophole-system.md#current-values, the retired-top-level-keys row),
// and with `host_processes` already gone
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
func validateHostWrappers(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
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
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(hostWrappersKey); atWorkspace && wsValue != nil {
		add(errs, "config."+hostWrappersKey+": user-scope only — it puts generated "+
			"executables on your PATH, so it is read from "+paths.UserConfigDisplayPath()+
			" and a workspace value has no effect. Move it there, or remove it.")
	}
}

// validateHostApplyOnLaunch shape-checks the `host_apply_on_launch` opt-in.
//
// A plain boolean for host_wrappers' reason: what the key decides is whether the mechanism
// runs at all, not which surfaces or which agents it covers — two tiers of "up to date" was
// refused outright (docs/reference/host-apply-staleness.md OQ-HS4), so there is nothing for a
// list to enumerate.
//
// The scope half is the same defense-in-depth host_wrappers takes, and the leak it guards is
// larger: the key licenses a WRITE into the real $HOME as a side effect of launching an agent,
// so a workspace value that merely looked accepted would be a silent no-op on the one class of
// key where a silent no-op is indistinguishable from a working exploit.
func validateHostApplyOnLaunch(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(hostApplyOnLaunchKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key here proves the
		// workspace config has none either — no re-read needed.
		return
	}
	if v != nil && !isBool(v) {
		add(errs, "config."+hostApplyOnLaunchKey+": expected a boolean (got "+pyReprValue(v)+")")
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(hostApplyOnLaunchKey); atWorkspace && wsValue != nil {
		add(errs, "config."+hostApplyOnLaunchKey+": user-scope only — it lets a wrapped agent "+
			"launch write into your real home, so it is read from "+paths.UserConfigDisplayPath()+
			" and a workspace value has no effect. Move it there, or remove it.")
	}
}

// validateHostManagement shape-checks the `host_management` ownership declaration
// (docs/reference/config-ownership.md#invariants).
//
// A string rather than a boolean, though it has two values since `assert` was retired
// (docs/reference/config-ownership.md#the-retired-assert, OQ-CO14): a config still spelling `"assert"` is
// refused with a message of its own (hostManagementProblem), which a boolean could not have
// told apart from a typo. The accepted set comes from KnownHostManagements, so the schema and
// the message cannot drift.
//
// The scope half is the same defense-in-depth `host_apply_on_launch` takes, and the claim it
// guards is the largest of the four: the key decides whether yolo may write the real $HOME at
// all, and at `own` whether the user's hand-written keys become derived output. A workspace
// value that merely LOOKED accepted would be indistinguishable from a cloned repository
// successfully declaring itself the owner of its user's home.
func validateHostManagement(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(hostManagementKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key here proves the
		// workspace config has none either — no re-read needed.
		return
	}
	if v != nil {
		if prob := hostManagementProblem(v); prob != "" {
			add(errs, "config."+hostManagementKey+": "+prob)
		}
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(hostManagementKey); atWorkspace && wsValue != nil {
		add(errs, "config."+hostManagementKey+": user-scope only — it declares who owns the "+
			"config files in your real home, so it is read from "+paths.UserConfigDisplayPath()+
			" and a workspace value has no effect. Move it there, or remove it.")
	}
}

// validateAgentUpdates shape-checks the `agent_updates` opt-out
// (docs/design/program-delivery.md §3.5, OQ-PD12).
//
// NOT a plain boolean, unlike its two neighbours, and the difference is real rather than
// stylistic: `host_wrappers` and `host_apply_on_launch` each decide whether ONE mechanism
// runs at all, while this one is per PACK — the user who wants their agents current but
// one of them frozen has a case those keys do not have. So both shapes are accepted, and
// the object's values must be booleans or the entry is a setting that reads as "on". Either
// shape may also carry a TIMING string, "launch" or "next-launch" (program-delivery.md
// OQ-PD30), and any other string is refused for the same reason.
//
// The scope half is the same defense-in-depth, and the leak it guards is the sharpest of
// the three: the value is read from user scope directly (AgentUpdatesWire), because
// /workspace is bind-mounted rw and a workspace-scoped spelling would let whatever is
// running IN the jail freeze its own updates. A value that merely looked accepted would be
// indistinguishable from a working one.
func validateAgentUpdates(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(agentUpdatesKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key here proves the
		// workspace config has none either — no re-read needed.
		return
	}
	if v != nil {
		if prob := agentUpdatesProblem(v); prob != "" {
			add(errs, "config."+agentUpdatesKey+": "+prob)
		}
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(agentUpdatesKey); atWorkspace && wsValue != nil {
		add(errs, "config."+agentUpdatesKey+": user-scope only — /workspace is writable by "+
			"whatever runs in the jail, so a workspace value would let an agent freeze its "+
			"own updates. It is read from "+paths.UserConfigDisplayPath()+" and a workspace value "+
			"has no effect. Move it there, or remove it.")
	}
}

// validateHostFloor shape-checks the `host_floor` opt-out (docs/reference/host-agent-environment.md
// OQ-HP1): `agent_updates`' two boolean shapes, without its timing values (a floor has no
// timing), and user scope only — the floor installs programs the host runs with the user's
// authority, so a workspace spelling (which an agent can edit) is refused rather than left looking
// as if it worked.
func validateHostFloor(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(hostFloorKey)
	if !present {
		return
	}
	if v != nil {
		if prob := packBoolPolicyProblem(v); prob != "" {
			add(errs, "config."+hostFloorKey+": "+prob)
		}
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(hostFloorKey); atWorkspace && wsValue != nil {
		add(errs, "config."+hostFloorKey+": user-scope only — it decides which programs yolo "+
			"installs on your machine and `yolo host` runs, so it is read from "+
			paths.UserConfigDisplayPath()+" and a workspace value has no effect. Move it there, or remove it.")
	}
}

// validateHostPath checks the `host_path` list (docs/reference/host-agent-environment.md, the launch PATH): a
// list of folders, each absolute or starting with `~/`, none relative, `~user/`, or carrying `$`
// or `:`. User scope only: a folder here decides which binary `yolo host` runs, and a workspace
// config is agent-editable, so a workspace spelling is refused rather than left looking as if it
// worked (HostPathFolders never reads it).
func validateHostPath(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(hostPathKey)
	if !present {
		return
	}
	if v != nil {
		for _, prob := range hostPathProblems(v) {
			add(errs, "config."+hostPathKey+": "+prob)
		}
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(hostPathKey); atWorkspace && wsValue != nil {
		add(errs, "config."+hostPathKey+": user-scope only — a folder on it decides which program "+
			"`yolo host` runs, so it is read from "+paths.UserConfigDisplayPath()+" and a workspace value "+
			"has no effect. Move it there, or remove it.")
	}
}

// ConvertLaterKey is the old user file's "stop offering the conversion" key (RC-D21,
// docs/design/config-migration.md §1): a person writes `"convert_later": true` by hand in the
// old-format user config, and the launch stops offering `yolo config migrate`. No command writes
// it, and a TOML file refuses it (readablevalidate.go), since a TOML file has nothing to convert.
const ConvertLaterKey = "convert_later"

// withoutConvertLater is config without ConvertLaterKey, for the unknown-key reporters; config
// itself is never changed.
func withoutConvertLater(config *jsonx.OrderedMap) *jsonx.OrderedMap {
	if _, present := config.Get(ConvertLaterKey); !present {
		return config
	}
	out := jsonx.NewOrderedMap()
	for _, key := range config.Keys() {
		if key != ConvertLaterKey {
			value, _ := config.Get(key)
			out.Set(key, value)
		}
	}
	return out
}

// validateConvertLater accepts `convert_later` only as a boolean in the user config, and refuses
// it in a workspace file: the offer it silences is the user's, and every old file this machine
// reads stops being offered at once, so a project has nothing to say about it.
func validateConvertLater(config *jsonx.OrderedMap, workspace string, errs *[]string, edit *editValidationContext) {
	v, present := config.Get(ConvertLaterKey)
	if !present {
		return
	}
	if _, ok := v.(bool); !ok {
		add(errs, "config."+ConvertLaterKey+": expected true or false (got "+reprAny(v)+"). "+
			"Write \"convert_later\": true in "+paths.UserConfigDisplayPath()+" to stop the launch offering "+
			"`yolo config migrate`, or remove the key.")
	}
	var wsCfg *jsonx.OrderedMap
	var err error
	if edit != nil {
		wsCfg, err = loadValidationWorkspaceConfig(workspace, edit)
	} else {
		wsCfg, err = loadValidationWorkspaceConfig(workspace)
	}
	if err != nil || wsCfg == nil {
		return
	}
	if _, atWorkspace := wsCfg.Get(ConvertLaterKey); atWorkspace {
		add(errs, "config."+ConvertLaterKey+": user-scope only — it silences the launch's offer to "+
			"convert every old-format file this machine reads, which is the user's choice, not a "+
			"project's. Move it to "+paths.UserConfigDisplayPath()+", or remove it.")
	}
}

// validateUpdateCheck type-checks `update_check` and refuses the workspace
// spelling, which UpdateCheckEnabled never reads (see its doc for why).
func validateUpdateCheck(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(updateCheckKey)
	if !present {
		return
	}
	if v != nil {
		if prob := updateCheckProblem(v); prob != "" {
			add(errs, "config."+updateCheckKey+": "+prob)
		}
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(updateCheckKey); atWorkspace && wsValue != nil {
		add(errs, "config."+updateCheckKey+": user-scope only — it is read on every host "+
			"command, before any workspace config is loaded, and /workspace is writable from "+
			"inside a jail, so a workspace value is never consulted. It is read from "+
			paths.UserConfigDisplayPath()+". Move it there, or remove it. (Per-shell instead: "+
			"YOLO_NO_UPDATE_CHECK=1.)")
	}
}

// validatePerfLogging type-checks `perf_logging` and refuses the workspace
// spelling. Both halves are defense-in-depth against a value that LOOKS accepted:
// the key is read from user scope directly (PerfLoggingEnabled), at the top of
// run.Run before any merged config exists, so a workspace value would not be
// consulted no matter how well-formed it is. Saying so beats a silent no-op.
func validatePerfLogging(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(perfLoggingKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key here
		// proves the workspace config has none either — no re-read needed.
		return
	}
	if v != nil {
		if prob := perfLoggingProblem(v); prob != "" {
			add(errs, "config."+perfLoggingKey+": "+prob)
		}
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(perfLoggingKey); atWorkspace && wsValue != nil {
		add(errs, "config."+perfLoggingKey+": user-scope only — it is read at the top of "+
			"a launch, before any workspace config is loaded, so a workspace value is "+
			"never consulted. It is read from "+paths.UserConfigDisplayPath()+". Move it there, "+
			"or remove it. (Per-launch instead: `yolo --timing`, or YOLO_TIMING=1.)")
	}
}

// validatePromotionTarget checks the host-side default for `yolo config promote`.
// A workspace is jail-writable, so it may not redirect a user's captured choices into a
// pack chosen by that workspace; the reader independently reads user scope only.
func validatePromotionTarget(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(promotionTargetKey)
	if !present {
		return
	}
	if v != nil {
		if problem := promotionTargetProblem(v); problem != "" {
			add(errs, "config."+promotionTargetKey+": "+problem)
		}
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(promotionTargetKey); atWorkspace && wsValue != nil {
		add(errs, "config."+promotionTargetKey+": user-scope only — it selects the pack "+
			"that receives your captured personal settings, so it is read from "+paths.UserConfigDisplayPath()+
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

// validateMacosLogRetired reports the RETIRED top-level `macos_log` key.
//
// The key dialled a `yolo-log` wrapper that ran Apple's `/usr/bin/log` INSIDE the macos-user
// sandbox, and that could not work: the sandbox account cannot read the unified log even with
// no Seatbelt profile (macos-user CI run 37940733418; packs/macos-log/README.md). The log is
// now read on the host, by the `macos-log` loophole of the official `macos-log` pack, so the
// key went the way `journal` did: core's schema names no loophole.
//
// A REFUSAL, not silence, for validateJournalRetired's reason: the key turned a capability on,
// and a config still asking for it must be told where it went. The message carries the three
// steps (select the pack, enable the loophole, and for the old "full" the user-scope setting).
// `"macos_log": "off"` asked for nothing, but it is refused all the same: one rule for one key,
// and the fix — delete the line — is in the message.
//
// ERROR ON THE HOST, WARNING INSIDE A JAIL, for validateJournalRetired's reason.
func validateMacosLogRetired(config *jsonx.OrderedMap, errs, warns *[]string) {
	if _, present := config.Get("macos_log"); !present {
		return
	}
	msg := "config.macos_log: REMOVED — the macOS unified log is now read on the host by the " +
		"`macos-log` loophole, because the macos-user sandbox account cannot read it itself. " +
		"Delete the key; to read the log from a jail, add `macos-log` to `packs` in " +
		"~/.config/yolo-jail/config.jsonc (beside your agents, e.g. " +
		`["claude", "macos-log"]) and set "loopholes": {"macos-log": {"enabled": true}}, which ` +
		`is what "macos_log": "user" was meant to give (entries from the sandbox's own ` +
		`processes). The old "full" is one more key — "loopholes": {"macos-log": ` +
		`{"settings": {"full": true}}} — and it is USER-CONFIG-ONLY: ` +
		"~/.config/yolo-jail/config.jsonc. Then run `yolo check`."
	if inJail() {
		add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
			"so remove the key from the HOST config.)")
		return
	}
	add(errs, msg)
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
// (docs/reference/loophole-system.md#current-values, the retired-top-level-keys row),
// and that is what made "convert the
// loophole to a pack" a separation in appearance only: the manifest would move out
// of core while core's schema went on naming it. The keys now live in the loophole's
// own manifest, in the official `host-processes` pack, under
// `loopholes.host-processes.settings` (docs/reference/pack-system.md).
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

// MCPPresetsRetiredMessage is the refusal of the retired `mcp_presets` key, with both next
// steps (docs/design/mcp-presets-removal.md §13 step 3). `yolo host apply`, which runs no full
// validation, names it too (composeHostInputs).
const MCPPresetsRetiredMessage = "config.mcp_presets: REMOVED — yolo no longer keeps its own list of MCP servers. " +
	"Delete the key. For the browser (`chrome-devtools`), add `chrome-devtools` to `packs` in " +
	"~/.config/yolo-jail/config.jsonc (beside your agents, e.g. " +
	`["claude", "chrome-devtools"]); a workspace file cannot select it. For ` +
	"`sequential-thinking`, which was deleted, write the server yourself: " +
	`"mcp_servers": {"sequential-thinking": {"command": "npx", "args": ["-y", ` +
	`"@modelcontextprotocol/server-sequential-thinking"]}}. Then run ` + "`yolo check`."

// validateMCPPresetsRetired reports the DELETED top-level `mcp_presets` key (2026-10-23,
// docs/design/mcp-presets-removal.md §13 step 3, ruled at docs/reference/pack-system.md#oq-pk1).
// It was core's own list of which MCP servers exist, two names long: `chrome-devtools` is the
// pack of that name now, selected in the user config, and `sequential-thinking` was deleted
// with no replacement beyond the user's own `mcp_servers` entry (§4).
//
// A REFUSAL, NOT SILENCE, for validateJournalRetired's reason: the key turned a server on, and
// a config still asking for the browser and getting nothing has been silently denied it (§9,
// OQ-MP6). Every value is refused, `[]` and null included — the key being present is the
// whole condition — and type checks went with the key.
//
// ERROR ON THE HOST, WARNING INSIDE A JAIL, for validateJournalRetired's reason: in-jail the
// config is the host-generated snapshot, which an older host may still have written it into.
func validateMCPPresetsRetired(config *jsonx.OrderedMap, errs, warns *[]string) {
	if _, present := config.Get("mcp_presets"); !present {
		return
	}
	msg := MCPPresetsRetiredMessage
	if inJail() {
		add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
			"so remove the key from the HOST config.)")
		return
	}
	add(errs, msg)
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

// validatePackMCPServers refuses an MCP server name two selected packs ship (packdecl.KindMCP is
// sole-owned by server name): the composed mcp_servers table is keyed by it, so the composer's
// later-wins rule would hand every agent one pack's server under a name the other pack also
// claims, with nothing said. The selection is the user scope's (resolveSelectedPacks, the one
// validation reserves names for), so the launch and `yolo check` refuse alike; a selection that
// cannot be read refuses nothing here, its own failure being louder elsewhere.
func validatePackMCPServers(errs *[]string, edit ...*editValidationContext) {
	var packs []*packload.Pack
	if len(edit) != 0 && edit[0] != nil {
		packs = edit[0].packs
	} else {
		packs, _ = resolveSelectedPacks()
	}
	for _, msg := range packload.MCPNameCollisions(packs) {
		add(errs, "config.packs: "+msg)
	}
}

// validatePrune checks the `prune` block: an object, a one-key census, and a positive
// number for the threshold.
//
// WHY IT DID NOT EXIST, since the absence is the interesting part: `prune` was added to
// knownTopLevelConfigKeys (config.go) and never given a validator, so the top-level
// census accepted the NAME and nothing looked inside. `prune: "hello"` validated. The
// coverage test that should have caught the missing documentation passed on a substring
// — "prune" is inside "autoprune" — so both halves of the gap held each other up.
//
// The threshold is compared against `>= 15.0` GiB by checkDiskUsage, which silently
// ignores a zero or negative value (`f > 0`). That ignoring is correct at the read site
// and is why the refusal belongs here: a user who writes 0 meaning "never warn" has
// asked for something this key cannot express, and the honest answer is to say so
// rather than to apply 15.
func validatePrune(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("prune")
	if !present || v == nil {
		return
	}
	prune, ok := asMap(v)
	if !ok {
		add(errs, "config.prune: expected an object")
		return
	}
	reportUnknownKeys(prune, knownPruneKeys, "config.prune", errs)
	raw, ok := prune.Get("warn_threshold_gb")
	if !ok || raw == nil {
		return
	}
	// int or float only, matching what the READER accepts — checkDiskUsage's
	// numberFloat takes a decoded JSON int or float and deliberately not a bool, so a
	// validator that accepted a string here would pass a value the reader then ignores,
	// which is the silent drop this whole function exists to end.
	const msg = "config.prune.warn_threshold_gb: expected a positive number of GiB (e.g. 25)"
	switch {
	case isBool(raw):
		add(errs, msg)
	default:
		if n, ok := jsonx.AsInt(raw); ok {
			if n <= 0 {
				add(errs, msg)
			}
			return
		}
		if f, ok := raw.(float64); ok {
			if f <= 0 {
				add(errs, msg)
			}
			return
		}
		add(errs, msg)
	}
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
	// The disk I/O priority, through the SAME reader the launcher, the briefing, the
	// macos-user line and `yolo check` use, so the shorthand cannot be accepted here and
	// read differently there (docs/design/io-priority.md §4, IO-D3).
	if ioV, _ := resources.Get("io"); ioV != nil {
		_, problems := ioprio.Parse(ioV, "config.resources.io")
		for _, p := range problems {
			add(errs, p)
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
func validateCacheRelocations(config *jsonx.OrderedMap, workspace string, errs, warns *[]string, edit ...*editValidationContext) {
	v, present := config.Get(cacheRelocationsKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key
		// here proves the workspace config has none either — no re-read needed.
		return
	}
	// Warnings from the re-read are discarded: this same file was already
	// loaded (and any parse problem already reported) by whoever produced the
	// merged config we were handed.
	if wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...); err == nil && wsCfg != nil {
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

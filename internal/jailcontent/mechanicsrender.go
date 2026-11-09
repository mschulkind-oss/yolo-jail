package jailcontent

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
)

// renderBuiltinSkillFiles is the staging seam; tests inject a failure to verify
// that the previous composed tree remains intact.
var renderBuiltinSkillFiles = RenderBuiltinSkills

// not inspect the filesystem, environment, or runtime.
func RenderBuiltinSkills(facts FactSheet) (map[string][]byte, error) {
	out := make(map[string][]byte)
	for _, name := range []string{"configuring-the-jail", "diagnosing-the-jail"} {
		body, err := renderExistingBuiltin(name, facts)
		if err != nil {
			return nil, err
		}
		out[filepath.ToSlash(filepath.Join(name, "SKILL.md"))] = body
	}
	mechanics, err := renderMechanicsSkill(facts)
	if err != nil {
		return nil, err
	}
	for name, body := range mechanics {
		out[name] = []byte(body)
	}
	return out, nil
}

// renderExistingBuiltin keeps the full existing syntax and triage guidance while
// substituting paths with the exact values from the fact sheet.
func renderExistingBuiltin(name string, facts FactSheet) ([]byte, error) {
	if facts.Host {
		workspace := facts.HostWorkspace
		if workspace == "" {
			workspace = "the host workspace"
		}
		configPath := facts.UserConfigPath
		if configPath == "" {
			configPath = "the user-scope config file"
		}
		if name == "configuring-the-jail" {
			return []byte("---\nname: configuring-the-jail\ndescription: \"Change applied host environment settings.\"\n---\n\n# Host environment\n\n## Apply a host configuration change\n\n1. Edit the user configuration at `" + configPath + "`. Use `yolo config-ref` for accepted keys and their scope.\n2. Run `yolo check` from `" + workspace + "` and resolve every reported failure.\n3. Run `yolo host apply` from `" + workspace + "`. Its output is the record of what changed.\n"), nil
		}
		log := facts.LogPath
		if log == "" {
			log = "the host log path reported by yolo"
		}
		return []byte("---\nname: diagnosing-the-jail\ndescription: \"Diagnose host configuration and apply failures.\"\n---\n\n# Diagnose host configuration\n\n## 1. Validate before applying\n\nRun `yolo check` from `" + workspace + "`. Resolve each `[FAIL]` before applying; the check output names the config key and remedy.\n\n## 2. Inspect the host apply\n\nRun `yolo host apply` from `" + workspace + "` and read its report for the exact files and skills applied. If it reports a failure, inspect the host log at `" + log + "` and address the named source or permission problem before retrying.\n"), nil
	}
	body, err := builtinskills.FS.ReadFile(name + "/SKILL.md")
	if err != nil {
		return nil, err
	}
	text := string(body)
	text = strings.ReplaceAll(text, "# Configuring the Jail", "# Configuring the environment")
	text = strings.ReplaceAll(text, "# Diagnose the Jail", "# Diagnose this environment")
	workspace := facts.WorkspacePath
	if workspace == "" {
		workspace = "the workspace"
	}
	if name == "configuring-the-jail" {
		text = replaceSection(text, "## You already know the loop — here's what it leaves out", "### Which file — and how the layers merge",
			"## Change timing\n\nBefore editing the config, read **jail-mechanics** for the applied facts and exact next steps. The change-timing recipe is generated from this jail's launch decisions.\n\n")
		text = strings.ReplaceAll(text, "podman's `-p`", "the container runtime's port mapping")
		text = strings.ReplaceAll(text, "podman is host-first", "the port order is host-first")
		text = strings.ReplaceAll(text, ",\n  or `.yolo/setups/<setup>/home/.pi` on Apple Container", "")
		if facts.Briefing.Mechanism == "macos-user" {
			text = strings.ReplaceAll(text, "Use this when a change must be **baked into how the jail is built** so it", "Use this when a change must be part of the permanent environment configuration so it")
			text = replaceSection(text, "## Two representative edits (then run `yolo check --no-build`)", "## Some files in your home are GENERATED — don't hand-edit them", "## Two representative edits (then run `yolo check --no-build`)\n\nUse **jail-mechanics** for the exact current package delivery and change steps.\n\n")
			text = replaceSection(text, "### 2. A `mounts` entry lands at `/ctx/<basename>` — of the RESOLVED path", "### 3. A string `mounts` entry is read-only, and `:ro` is NOT a suffix you write", "")
			text = replaceSection(text, "### 3. A string `mounts` entry is read-only, and `:ro` is NOT a suffix you write", "### 4. An in-jail `yolo check` CANNOT judge a `mounts` path", "")
			text = replaceSection(text, "### 4. An in-jail `yolo check` CANNOT judge a `mounts` path", "### 6. Host `~` is NOT jail `/home/agent` — dotfiles do not project into the jail", "")
			text = replaceSection(text, "### 5. Networking: name the DIRECTION before you touch a port key", "### 6. Host `~` is NOT jail `/home/agent` — dotfiles do not project into the jail", "")
			text = replaceSection(text, "### 6. Host `~` is NOT jail `/home/agent` — dotfiles do not project into the jail", "## Don't guess at keys — the schema lives in the CLI", "")
			text = substituteBuiltinPaths(text, workspace, facts.Briefing.Home)
			start := strings.Replace("### 1. The directory you launch from IS `/workspace`.", "/workspace", workspace, 1)
			appliedWorkspace := facts.WorkspacePath
			if appliedWorkspace == "" {
				appliedWorkspace = "the applied workspace path"
			}
			text = replaceSection(text, start, "## Don't guess at keys — the schema lives in the CLI", "### Workspace path\n\nThis workspace is the host directory `"+appliedWorkspace+"`; it is not a container bind mount.\n\n")
		} else if networkSharesHost(appliedNetworkMode(facts)) {
			text = replaceSection(text, "### 5. Networking: name the DIRECTION before you touch a port key", "### 6. Host `~` is NOT jail `/home/agent` — dotfiles do not project into the jail",
				"### 5. Networking: this jail shares the host network stack\n\nThis jail uses host networking. Its processes share the host network stack, including loopback; there is no separate jail loopback and container port mappings do not apply.\n\n")
		}
		if facts.Briefing.Mechanism != "macos-user" {
			text = substituteBuiltinPaths(text, workspace, facts.Briefing.Home)
		}
		if facts.ConfigPath != "" {
			userConfig := facts.UserConfigPath
			if userConfig == "" {
				userConfig = "the user-scope configuration file"
			}
			state := "The agent may edit this workspace config."
			if facts.Briefing.ConfigLocked {
				state = "This workspace config is read-only to the agent. Do not edit this file from the jail; tell the human the exact path and requested change."
			}
			localState := "No workspace-local override was present for this launch."
			if facts.LocalConfigPresent && facts.LocalConfigPath != "" {
				localState = "The workspace-local override present for this launch is `" + facts.LocalConfigPath + "` and takes precedence over the workspace config."
			} else if facts.LocalConfigPresent {
				localState = "A workspace-local override was present for this launch; its resolved path is unavailable in this fact sheet."
			}
			if !facts.WorkspaceConfigPresent {
				localState += " The resolved workspace config was not present when this launch was prepared."
			}
			text = replaceSection(text, "### Which file — and how the layers merge", "## Two representative edits (then run `yolo check --no-build`)",
				"### Which file — and how the layers merge\n\nThe user-scope configuration is `"+userConfig+"`. The workspace configuration this launch reads is `"+facts.ConfigPath+"` (`"+configName(facts)+"`). "+state+" "+localState+" The user config is the machine-wide base; workspace files add project-specific settings.\n\n")
		}
	}
	if name == "diagnosing-the-jail" {
		text = substituteBuiltinPaths(text, workspace, facts.Briefing.Home)
		log := facts.LogPath
		if log == "" {
			log = "the workspace's .yolo/logs/startup.log"
		}
		aclStart := "## 2b. macOS only: `permission denied` writing inside the workspace"
		keepACL := facts.Briefing.Mechanism == "macos-user" && facts.Briefing.IsMacOS && facts.NativeAccountMode == "account" && facts.NativeAccountUser != ""
		logEnd := "## 3. A tool is blocked or shimmed"
		if keepACL {
			logEnd = aclStart
		}
		text = replaceSection(text, "## 2. Provisioning failed → read the startup log", logEnd,
			"## 2. Provisioning failed → read the startup log\n\nRead the startup log at `"+log+"` and inspect the failure. Fix the failed tool or provision step, then retry the work.\n\n")
		acl := sectionBetween(text, aclStart, "## 3. A tool is blocked or shimmed")
		if keepACL {
			acl = strings.ReplaceAll(acl, "On the **macos-user** backend", "Here")
			acl = strings.ReplaceAll(acl, "**macos-user**", "this environment")
			acl = strings.ReplaceAll(acl, "## 2b. macOS only:", "## 2b.")
			text = replaceSection(text, aclStart, "## 3. A tool is blocked or shimmed", acl)
		} else {
			text = replaceSection(text, aclStart, "## 3. A tool is blocked or shimmed", "")
		}
		text = strings.ReplaceAll(text, "validates `yolo-jail.jsonc`", "validates the resolved workspace config `"+configName(facts)+"`")
		if facts.Briefing.Mechanism == "macos-user" {
			text = strings.ReplaceAll(text, "This is the fast in-jail preflight: it validates the resolved workspace config `"+configName(facts)+"` and the\nentrypoint without rebuilding the image.", "This command checks the resolved workspace config `"+configName(facts)+"` and entrypoint.")
			text = replaceSection(text, "## 5. A port isn't reachable → establish the DIRECTION first", "## 6. \"My fix isn't taking effect\" → you're running stale code", "")
		} else if networkSharesHost(appliedNetworkMode(facts)) {
			text = replaceSection(text, "## 5. A port isn't reachable → establish the DIRECTION first", "## 6. \"My fix isn't taking effect\" → you're running stale code",
				"## 5. A port isn't reachable\n\nThis jail shares the host network stack, including loopback. There is no separate jail loopback and container port mappings do not apply. Check that the service is listening on the expected host address and port.\n\n")
		}
		text = replaceSection(text, "## 6. \"My fix isn't taking effect\" → you're running stale code", "## 7. Orphans and logs",
			"## 6. My fix isn't taking effect\n\nRun `yolo config drift` to see pending changes, then read the **jail-mechanics** skill for this jail's applied state and next steps.\n\n")
	}
	return []byte(text), nil
}

// substituteBuiltinPaths rewrites embedded prose without reprocessing inserted fact values.
func substituteBuiltinPaths(text, workspace, home string) string {
	replacements := []string{"/workspace", workspace}
	if home != "" {
		replacements = append(replacements, "/home/agent", home)
	}
	return strings.NewReplacer(replacements...).Replace(text)
}

func replaceSection(content, start, end, replacement string) string {
	section := sectionBetween(content, start, end)
	if section == "" {
		return content
	}
	return strings.Replace(content, section, replacement, 1)
}

func sectionBetween(content, start, end string) string {
	a := strings.Index(content, start)
	if a < 0 {
		return ""
	}
	b := strings.Index(content[a+len(start):], end)
	if b < 0 {
		return ""
	}
	return content[a : a+len(start)+b]
}

type mechanicsRecipe struct{ Title, File string }

func mechanicsRecipes(facts FactSheet) []mechanicsRecipe {
	all := []mechanicsRecipe{{"Workspace config", "workspace-config.md"}, {"User-scope config", "user-config.md"},
		{"Packs and loopholes", "packs-and-loopholes.md"}, {"Skills and briefing", "skills-and-briefing.md"},
		{"Host files", "host-files.md"}, {"Tools", "tools.md"}, {"Restart", "restart.md"}, {"Host yolo update", "yolo-update.md"}}
	var out []mechanicsRecipe
	for _, item := range all {
		if recipeApplies(item.File, facts) {
			out = append(out, item)
		}
	}
	return out
}

func renderMechanicsSkill(facts FactSheet) (map[string]string, error) {
	indexMap, err := renderMechanicsIndexAndMap(facts)
	if err != nil {
		return nil, err
	}
	files := map[string]string{
		"jail-mechanics/SKILL.md":    string(indexMap["jail-mechanics/SKILL.md"]),
		"jail-mechanics/host-map.md": string(indexMap["jail-mechanics/host-map.md"]),
	}
	for _, r := range mechanicsRecipes(facts) {
		files["jail-mechanics/recipes/"+r.File] = renderRecipe(r.File, facts)
	}
	return files, nil
}

func renderMechanicsIndexAndMap(facts FactSheet) (map[string][]byte, error) {
	recipes := mechanicsRecipes(facts)
	raw, err := builtinskills.FS.ReadFile("templates/index.tmpl")
	if err != nil {
		return nil, err
	}
	t, err := template.New("index").Parse(string(raw))
	if err != nil {
		return nil, err
	}
	var index bytes.Buffer
	if err := t.Execute(&index, struct {
		Host                                                 bool
		HostWorkspace, WorkspacePath, ConfigPath, ConfigName string
		ConfigLocked                                         bool
		RestartCommand                                       string
		Recipes                                              []mechanicsRecipe
	}{facts.Host, facts.HostWorkspace, facts.WorkspacePath, facts.ConfigPath, configName(facts), facts.Briefing.ConfigLocked, facts.RestartCommand, recipes}); err != nil {
		return nil, err
	}
	return map[string][]byte{"jail-mechanics/SKILL.md": index.Bytes(), "jail-mechanics/host-map.md": []byte(renderPathMap(facts))}, nil
}

func configName(facts FactSheet) string {
	if facts.Briefing.ConfigName != "" {
		return facts.Briefing.ConfigName
	}
	if facts.ConfigPath != "" {
		return filepath.Base(facts.ConfigPath)
	}
	return "the workspace config file"
}

func recipeApplies(file string, facts FactSheet) bool {
	switch file {
	case "workspace-config.md":
		return !facts.Host && facts.ConfigPath != "" && facts.HostWorkspace != ""
	case "user-config.md":
		return facts.UserConfigPath != "" && facts.HostWorkspace != ""
	case "packs-and-loopholes.md":
		return !facts.Host && facts.UserConfigPath != "" && facts.HostWorkspace != "" && (len(facts.Packs) > 0 || len(facts.Briefing.Loopholes) > 0)
	case "skills-and-briefing.md":
		return facts.SkillsDelivery != ""
	case "host-files.md":
		return len(facts.Paths) > 0
	case "tools.md":
		return facts.PackageDelivery != "" && (!facts.Host || facts.HostWorkspace != "")
	case "restart.md":
		return !facts.Host && facts.RestartCommand != "" && facts.HostWorkspace != ""
	case "yolo-update.md":
		return !facts.Host && facts.RestartCommand != "" && facts.HostWorkspace != ""
	default:
		return false
	}
}

func appliedNetworkMode(facts FactSheet) string {
	if facts.AppliedNetworkMode != "" {
		return facts.AppliedNetworkMode
	}
	return facts.Briefing.AppliedNetMode
}

func networkSharesHost(mode string) bool {
	return mode == "host" || mode == "shared"
}

func renderPathMap(facts FactSheet) string {
	var b strings.Builder
	b.WriteString("# Paths in this environment\n\n")
	if facts.Setup != "" || facts.SetupDescription != "" {
		fmt.Fprintf(&b, "Selected setup: `%s`", facts.Setup)
		if facts.SetupDescription != "" {
			fmt.Fprintf(&b, " — %s", facts.SetupDescription)
		}
		b.WriteString(".\n\n")
	}
	b.WriteString("| Path | Original host source | Backing source | Mode | Lifetime / class | Delivery |\n|---|---|---|---|---|---|\n")
	paths := append([]PathFact(nil), facts.Paths...)
	sort.SliceStable(paths, func(i, j int) bool { return paths[i].Path < paths[j].Path })
	for _, p := range paths {
		original := p.OriginalSource
		if original == "" {
			original = p.HostSource
		}
		backing := p.BackingSource
		if original == "" {
			original = "—"
		}
		if backing == "" {
			backing = "—"
		}
		if p.Mode == "" {
			p.Mode = "—"
		}
		if p.Lifetime == "" {
			p.Lifetime = "—"
		}
		if p.Delivery == "" {
			p.Delivery = "—"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %s | %s | %s |\n", p.Path, original, backing, p.Mode, p.Lifetime, p.Delivery)
	}
	if facts.AppliedNetworkMode != "" {
		fmt.Fprintf(&b, "\nApplied network mode: %s.\n", facts.AppliedNetworkMode)
	}
	if facts.NixDelivery != "" {
		fmt.Fprintf(&b, "\nNix delivery: %s.\n", facts.NixDelivery)
	}
	if facts.HostLoopback != "" {
		fmt.Fprintf(&b, "\nHost loopback forwarding: %s.\n", facts.HostLoopback)
	}
	if facts.HomeRestPolicy != "" {
		fmt.Fprintf(&b, "\nHome rest-of-tree policy: %s.\n", facts.HomeRestPolicy)
	}
	if facts.Persistence != nil {
		if facts.Persistence.PerLaunchInRAM {
			b.WriteString("\nPer-launch paths are RAM-backed and are removed when the launch ends.\n")
		}
		if kept := facts.Persistence.Of(PathWorkspaceDurable); len(kept) > 0 {
			fmt.Fprintf(&b, "\nKept per-workspace paths: %s.\n", strings.Join(kept, ", "))
		}
	}
	if len(facts.WorkspaceReadonly) > 0 {
		b.WriteString("\n## Workspace read-only paths\n\n")
		for _, item := range facts.WorkspaceReadonly {
			state := "read-only enforced"
			if !item.Enforced {
				state = "read-only requested but not enforced"
			}
			fmt.Fprintf(&b, "- `%s` — %s. %s\n", item.Path, state, item.Reason)
		}
	}
	if len(facts.WorkspaceProjectDirs) > 0 {
		b.WriteString("\n## Staged read-only project directories\n\n")
		for _, path := range facts.WorkspaceProjectDirs {
			fmt.Fprintf(&b, "- `%s`\n", path)
		}
	}
	return b.String()
}

func renderRecipe(file string, facts FactSheet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSuffix(strings.ReplaceAll(file, "-", " "), ".md"))
	switch file {
	case "workspace-config.md":
		configPath := facts.ConfigPath
		if facts.Briefing.ConfigLocked {
			local := ""
			if facts.LocalConfigPresent && facts.LocalConfigPath != "" {
				local = " A local config file is also applied: `" + facts.LocalConfigPath + "`."
			}
			fmt.Fprintf(&b, "The workspace config `%s` (`%s`) is read-only to the agent. Do not edit it from the jail. Give the human the exact requested change, as the `yolo config set`, `yolo config add` or `yolo request` command to run on the host, then run `yolo check --no-build` and resolve every failure. The human runs `yolo config apply` from `%s`.%s The command's report wins.\n\n", configPath, configName(facts), facts.HostWorkspace, local)
		} else {
			fmt.Fprintf(&b, "1. Edit `%s`, with a verb where one covers the key: `yolo config set <key> <value>` (`yolo config add` for a list, `yolo request <source> <capability>` for a broker capability).\n2. Run `yolo check --no-build` and resolve every failure.\n3. %s\n4. The command's report wins over the expected outcomes below.\n\n", configPath, changetiming.HostCommand("yolo config apply", facts.HostWorkspace, false))
		}
		type timingGroup struct {
			outcome changetiming.Outcome
			rows    []changetiming.Row
		}
		var groups []timingGroup
		byOutcome := map[changetiming.Outcome]int{}
		for _, row := range facts.Timing {
			if row.Outcome == changetiming.NotApplicable {
				continue
			}
			i, ok := byOutcome[row.Outcome]
			if !ok {
				i = len(groups)
				byOutcome[row.Outcome] = i
				groups = append(groups, timingGroup{outcome: row.Outcome})
			}
			groups[i].rows = append(groups[i].rows, row)
		}
		for _, group := range groups {
			keys := make([]string, 0, len(group.rows))
			for _, row := range group.rows {
				keys = append(keys, "`"+row.Key+"`")
			}
			fmt.Fprintf(&b, "## %s: %s\n\n", strings.Join(keys, ", "), group.outcome)
			step := 1
			if !facts.Briefing.ConfigLocked {
				fmt.Fprintf(&b, "%d. Validate the edit with `yolo check --no-build`.\n", step)
				step++
				fmt.Fprintf(&b, "%d. %s\n", step, changetiming.HostCommand("yolo config apply", facts.HostWorkspace, false))
				step++
			}
			switch group.outcome {
			case changetiming.AppliedNow:
				fmt.Fprintf(&b, "%d. Expected: the change applies now in every session after `yolo config apply`. %s The command's report wins.\n\n", step, group.rows[0].Reason)
			case changetiming.NextSession:
				if facts.Briefing.Mechanism == "macos-user" {
					fmt.Fprintf(&b, "%d. End this session, then start a fresh `yolo` launch from `%s` on the host. Expected: the change takes effect in the new session. %s\n\n", step, facts.HostWorkspace, group.rows[0].Reason)
				} else {
					fmt.Fprintf(&b, "%d. %s\n", step, changetiming.HostCommand("yolo config apply", facts.HostWorkspace, false))
					step++
					fmt.Fprintf(&b, "%d. Start a new `yolo` session from `%s` on the host. Expected: the change takes effect in that session. %s\n\n", step, facts.HostWorkspace, group.rows[0].Reason)
				}
			case changetiming.AtNextRestart:
				fmt.Fprintf(&b, "%d. %s\n", step, changetiming.HostCommand("yolo config apply", facts.HostWorkspace, false))
				step++
				fmt.Fprintf(&b, "%d. %s, then start a fresh `yolo` launch from `%s`. Expected: the change is applied at the next restart; the command's report wins.\n\n", step, changetiming.HostCommand(facts.RestartCommand, facts.HostWorkspace, true), facts.HostWorkspace)
			case changetiming.RestartAndRebuild:
				fmt.Fprintf(&b, "%d. %s\n", step, changetiming.HostCommand("yolo config apply", facts.HostWorkspace, false))
				step++
				fmt.Fprintf(&b, "%d. %s, then start a fresh `yolo` launch from `%s`. The image is rebuilt first. Expected: the change is applied after restart and rebuild; %s The command's report wins.\n\n", step, changetiming.HostCommand(facts.RestartCommand, facts.HostWorkspace, true), facts.HostWorkspace, group.rows[0].Reason)
			case changetiming.AtNextLaunch:
				fmt.Fprintf(&b, "%d. Start the next `yolo` launch from `%s` on the host. This jail is not running, so the launch reads the edit.\n\n", step, facts.HostWorkspace)
			case changetiming.UserScopeOnly:
				fmt.Fprintf(&b, "%d. Do not add these settings to the workspace config. Give the human the exact edit for `%s`; they run `yolo check` and start a fresh `yolo` launch from `%s`. Workspace `yolo config apply` does not apply user-scope settings.\n\n", step, facts.UserConfigPath, facts.HostWorkspace)
			case changetiming.HostSwitch:
				// `yolo loopholes enable|disable` are retired (docs/reference/loophole-grants.md
				// LG-D8): what a loophole may do is a grant, and whether it runs is its pack.
				var named []string
				for _, sw := range facts.Switches {
					if sw.Available {
						named = append(named, "`"+sw.Name+"`")
					}
				}
				which := "A loophole"
				if len(named) > 0 {
					which = strings.Join(named, ", ")
				}
				fmt.Fprintf(&b, "%d. %s runs whenever its pack is selected; `yolo loopholes enable` and `disable` are retired. For a grant this project needs, run `yolo request <pack> <kind>` here; the human runs `yolo config apply` from `%s` on the host. To run or stop one, the human runs `yolo pack add <pack>` or `yolo pack remove <pack>` on the host.\n", step, which, facts.HostWorkspace)
				step++
				if facts.Briefing.Mechanism == "macos-user" {
					fmt.Fprintf(&b, "%d. End this session and start a fresh `yolo` launch from `%s`; the pack selection is read at launch.\n\n", step, facts.HostWorkspace)
				} else {
					fmt.Fprintf(&b, "%d. Restart using `%s` so the pack selection is read at launch. The command's report wins.\n\n", step, facts.RestartCommand)
				}
			}
		}
		if len(facts.CooperativeCPUDefaults) > 0 {
			b.WriteString("## Native CPU resource guidance\n\n")
			fmt.Fprintf(&b, "On macos-user, `resources.cpus` sets cooperative parallelism defaults for `%s`. Environment layers can override these values; they guide participating tools and do not enforce a CPU cap.\n\n", strings.Join(facts.CooperativeCPUDefaults, "`, `"))
		}
		if len(facts.ScopeRestrictions) > 0 {
			b.WriteString("## Workspace scope restrictions\n\nThese current validator rules apply to workspace scope; other fields of the same settings may have different scope.\n\n")
			for _, rule := range facts.ScopeRestrictions {
				fmt.Fprintf(&b, "- `%s` — %s\n", rule.Path, rule.When)
			}
			b.WriteByte('\n')
		}
	case "user-config.md":
		fmt.Fprintf(&b, "1. Give the human the exact edit for `%s`.\n2. Ask them to run `yolo check` there.\n", facts.UserConfigPath)
		if facts.Host {
			fmt.Fprintf(&b, "3. Run `yolo host apply` from `%s`.\n", facts.HostWorkspace)
		} else if facts.Briefing.Mechanism == "macos-user" {
			fmt.Fprintf(&b, "3. End this session; start a fresh `yolo` launch from `%s` so it reads the user-scope change.\n", facts.HostWorkspace)
		} else {
			fmt.Fprintf(&b, "3. %s\n4. Restart the jail from `%s` so the new user-scope settings are loaded.\n", changetiming.HostCommand("yolo config apply", facts.HostWorkspace, false), facts.HostWorkspace)
		}
	case "packs-and-loopholes.md":
		fmt.Fprintf(&b, "Selected packs for this jail: %s. Edit the packs list in `%s`, run `yolo check` there, then start a fresh `yolo` launch from `%s`; workspace `yolo config apply` does not apply user-scope pack changes. For brokered repository scope, edit `brokered.<source>.repos`; its timing is shown for `brokered` in the workspace-config recipe. A loophole has no switch of its own: it runs whenever its pack is selected.\n", strings.Join(facts.Packs, ", "), facts.UserConfigPath, facts.HostWorkspace)
	case "skills-and-briefing.md":
		fmt.Fprintf(&b, "1. Edit the skill or briefing source, not its staged copy.\n2. Delivery is %s; the map lists exact paths and lifetimes.\n", facts.SkillsDelivery)
		if facts.Host {
			fmt.Fprintf(&b, "3. Run `yolo host apply` from `%s`.\n", facts.HostWorkspace)
		} else if facts.SkillsDelivery == "live filesystem" || strings.Contains(facts.SkillsDelivery, "bind") {
			fmt.Fprintf(&b, "3. The next `yolo` entry from `%s` refreshes the live staging.\n", facts.HostWorkspace)
		} else {
			fmt.Fprintf(&b, "3. Start a fresh `yolo` launch from `%s` to receive the updated copy.\n", facts.HostWorkspace)
		}
	case "host-files.md":
		b.WriteString("1. Edit the host source listed in `host-map.md`.\n2. Follow its recorded delivery mode and lifetime in the map.\n")
	case "tools.md":
		if facts.Host {
			fmt.Fprintf(&b, "Install tools using the host's normal package manager. For a permanent host configuration change, run `yolo check` and then `yolo host apply` from `%s`.\n", facts.HostWorkspace)
		} else {
			fmt.Fprintf(&b, "For a one-off tool, install it in the current environment; that installation follows the path lifetimes in `host-map.md`. Permanent tool delivery for this launch is %s.\n", facts.PackageDelivery)
			if facts.PackageDelivery == "baked image" {
				fmt.Fprintf(&b, "For a permanent image package, run `yolo config add install.nix <package> --project`, validate with `yolo check --no-build`, then ask the human to run `yolo config apply` and restart the jail. %s The image rebuilds first.\n", changetiming.HostCommand(facts.RestartCommand, facts.HostWorkspace, true))
			} else if facts.PackageDelivery == "store-delivered" {
				fmt.Fprintf(&b, "For a permanent package, run `yolo config add install.nix <package> --project`, validate with `yolo check --no-build`, ask the human to run `yolo config apply` from `%s`, then restart the jail. %s This delivery does not require an image rebuild.\n", facts.HostWorkspace, changetiming.HostCommand(facts.RestartCommand, facts.HostWorkspace, true))
			} else {
				fmt.Fprintf(&b, "For a permanent tool, update its environment config, validate with `yolo check`, then follow its timing class in `workspace-config`.\n")
			}
		}
	case "restart.md":
		b.WriteString("1. Save work that should survive the restart.\n")
		if facts.Briefing.Mechanism == "macos-user" {
			fmt.Fprintf(&b, "2. End this session.\n3. Run `yolo` in `%s` to start a fresh session.\n", facts.HostWorkspace)
		} else {
			fmt.Fprintf(&b, "2. %s\n3. Run `yolo` in `%s` to start the jail again.\n", changetiming.HostCommand(facts.RestartCommand, facts.HostWorkspace, true), facts.HostWorkspace)
		}
	case "yolo-update.md":
		b.WriteString("1. Ask the human to update the host yolo launcher.\n")
		if facts.Briefing.Mechanism == "macos-user" {
			fmt.Fprintf(&b, "2. End this session.\n3. Run `yolo` in `%s` again.\n", facts.HostWorkspace)
		} else {
			fmt.Fprintf(&b, "2. %s\n3. Run `yolo` in `%s` again.\n", changetiming.HostCommand(facts.RestartCommand, facts.HostWorkspace, true), facts.HostWorkspace)
		}
	}
	return b.String()
}

package run

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// hostClaudeFileArgs runs the host ~/.claude file mounting (2744-2797): mount
// each configured host_claude_files entry (default ["settings.json"]) plus any
// scripts referenced by settings.json (fileSuggestion/statusLine/hooks) that
// live under ~/.claude/. Claude only.
func (o *Options) hostClaudeFileArgs(cfg *jsonx.OrderedMap, rt string, in *assembleInput) []string {
	hostClaudeDir := filepath.Join(homeDir(), ".claude")
	claudeSelected := inStrSlice(in.agentsList, "claude")

	var effective []string
	if claudeSelected {
		if v, ok := cfg.Get("host_claude_files"); ok {
			for _, e := range asAnyList(v) {
				if s, ok := e.(string); ok {
					effective = append(effective, s)
				}
			}
		} else {
			effective = append(effective, config.DefaultHostClaudeFiles...)
		}
	}

	settingsFile := filepath.Join(hostClaudeDir, "settings.json")
	if claudeSelected && fileExists(settingsFile) {
		effective = appendSettingsScripts(effective, settingsFile, hostClaudeDir)
	}

	var args []string
	var mounted []string
	for _, fname := range effective {
		hostFile := filepath.Join(hostClaudeDir, fname)
		if isFile(hostFile) {
			args = append(args, ROFileMountArg(
				hostFile, "/ctx/host-claude/"+fname, in.wsState, "ctx-host-claude/"+fname, in.mountTargets, nil)...)
			mounted = append(mounted, fname)
		}
	}
	if len(mounted) > 0 {
		args = append(args, "-e", "YOLO_HOST_CLAUDE_FILES="+jsonDumpsStrings(mounted))
	}
	_ = rt
	return args
}

// hostPiFileArgs runs the host ~/.pi/agent file mounting (run_cmd.py:2851-2871):
// mount each configured host_pi_files entry (default ["settings.json"]) verbatim.
// Unlike claude, pi has no hooks/statusLine/fileSuggestion, so there is NO script
// auto-discovery — the settings.json three-way merge happens jail-side in
// configure_pi. Pi only.
func (o *Options) hostPiFileArgs(cfg *jsonx.OrderedMap, in *assembleInput) []string {
	if !inStrSlice(in.agentsList, "pi") {
		return nil
	}
	hostPiDir := filepath.Join(homeDir(), ".pi", "agent")

	var effective []string
	if v, ok := cfg.Get("host_pi_files"); ok {
		for _, e := range asAnyList(v) {
			if s, ok := e.(string); ok {
				effective = append(effective, s)
			}
		}
	} else {
		effective = append(effective, config.DefaultHostPiFiles...)
	}

	var args []string
	var mounted []string
	for _, fname := range effective {
		hostFile := filepath.Join(hostPiDir, fname)
		if isFile(hostFile) {
			args = append(args, ROFileMountArg(
				hostFile, "/ctx/host-pi/"+fname, in.wsState, "ctx-host-pi/"+fname, in.mountTargets, nil)...)
			mounted = append(mounted, fname)
		}
	}
	if len(mounted) > 0 {
		args = append(args, "-e", "YOLO_HOST_PI_FILES="+jsonDumpsStrings(mounted))
	}
	return args
}

// appendSettingsScripts walks settings.json for command paths
// (fileSuggestion.command, statusLine.command, hooks[*][*].hooks[*].command)
// that resolve under host_claude_dir, appending their basenames (dedup).
func appendSettingsScripts(effective []string, settingsFile, hostClaudeDir string) []string {
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		return effective
	}
	decoded, err := jsonx.Decode(data)
	if err != nil {
		return effective
	}
	settings, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return effective
	}
	var scriptCmds []string
	for _, key := range []string{"fileSuggestion", "statusLine"} {
		if sec, ok := settings.Get(key); ok {
			if m, ok := sec.(*jsonx.OrderedMap); ok {
				if c := mapStr(m, "command"); c != "" {
					scriptCmds = append(scriptCmds, c)
				}
			}
		}
	}
	if hooksV, ok := settings.Get("hooks"); ok {
		if hooks, ok := hooksV.(*jsonx.OrderedMap); ok {
			for _, ev := range hooks.Keys() {
				matchersV, _ := hooks.Get(ev)
				matchers, ok := matchersV.([]any)
				if !ok {
					continue
				}
				for _, mAny := range matchers {
					m, ok := mAny.(*jsonx.OrderedMap)
					if !ok {
						continue
					}
					inner, _ := m.Get("hooks")
					for _, hAny := range asAnyList(inner) {
						if h, ok := hAny.(*jsonx.OrderedMap); ok {
							if c := mapStr(h, "command"); c != "" {
								scriptCmds = append(scriptCmds, c)
							}
						}
					}
				}
			}
		}
	}
	for _, cmd := range scriptCmds {
		resolved := strings.ReplaceAll(cmd, "~", homeDir())
		if isUnderOrEqual(resolved, hostClaudeDir) {
			fname := filepath.Base(resolved)
			if !containsStr(effective, fname) {
				effective = append(effective, fname)
			}
		}
	}
	return effective
}

func containsStr(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

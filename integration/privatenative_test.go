package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// This is an external-constructor guard, NOT a hook in production children.
// Genuine workers reread the finite, controlled local fixture through their normal
// resolver and admission. They do not consume the test-private marker.
type privateNativeFixture struct {
	home, userConfig string
	packs            map[string]string
	check            func() error
	copyBinary       string
	prebuiltCertFile string
	forkPin          *privateNativeForkPin
}

var privateNative *privateNativeFixture

func usePrivateNativeFixture(t *testing.T, f *privateNativeFixture) {
	t.Helper()
	previous := privateNative
	privateNative = f
	t.Cleanup(func() { privateNative = previous })
}

func privateNativeOwnedPath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("private native input missing or linked: %s", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("private native input not owned: %s", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		return fmt.Errorf("private native input has linked ancestry: %s", path)
	}
	return nil
}

func privateNativeJSON(path, expected string) error {
	if err := privateNativeOwnedPath(path, false); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var actual, want any
	if err := json.Unmarshal(raw, &actual); err != nil {
		return fmt.Errorf("private native unreadable JSON: %s: %w", path, err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		return fmt.Errorf("private native invalid finite recipe: %w", err)
	}
	if !reflect.DeepEqual(actual, want) {
		return fmt.Errorf("private native selection/recipe drift: %s", path)
	}
	return nil
}

func privateNativeFiles(dir string, expected map[string]string) error {
	if err := privateNativeOwnedPath(dir, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != len(expected) {
		return fmt.Errorf("private native unclassified files in %s", dir)
	}
	for _, entry := range entries {
		want, known := expected[entry.Name()]
		if !known {
			return fmt.Errorf("private native unclassified file: %s", entry.Name())
		}
		path := filepath.Join(dir, entry.Name())
		if err := privateNativeOwnedPath(path, false); err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != want {
			return fmt.Errorf("private native fixture file drift: %s", path)
		}
	}
	return nil
}

func (f *privateNativeFixture) admit(env []string) error {
	home := launchHome(env)
	if home != f.home {
		return fmt.Errorf("private native HOME mismatch")
	}
	if err := privateNativeOwnedPath(home, true); err != nil {
		return err
	}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if value != environmentValue(env, name) {
			continue
		}
		switch name {
		case "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR":
			if value != "" && value != filepath.Join(home, map[string]string{"XDG_CONFIG_HOME": ".config", "XDG_DATA_HOME": ".local/share", "XDG_CACHE_HOME": ".cache", "XDG_RUNTIME_DIR": ".run"}[name]) {
				return fmt.Errorf("private native XDG drift: %s", name)
			}
		case "YOLO_USER_LAYER", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM":
			if value != "" {
				return fmt.Errorf("private native unclassified environment input: %s", name)
			}
		}
	}
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	entries, err := os.ReadDir(cfgDir)
	if err != nil || len(entries) < 1 || len(entries) > 5 {
		return fmt.Errorf("private native unclassified user config graph: %v", entries)
	}
	for _, entry := range entries {
		if entry.Name() != "config.jsonc" && (f.forkPin == nil || !f.forkPin.generatedInput(entry.Name())) {
			return fmt.Errorf("private native unclassified additional user config input: %s", entry.Name())
		}
	}
	if err := privateNativeJSON(filepath.Join(cfgDir, "config.jsonc"), f.userConfig); err != nil {
		return err
	}
	if f.check != nil {
		if err := f.check(); err != nil {
			return err
		}
	}
	if f.forkPin != nil {
		if err := f.forkPin.admit(filepath.Join(cfgDir, "config.jsonc")); err != nil {
			return err
		}
	}
	// These are the actual selection/manifest readers, with an observational local
	// resolver. No ResolvePack, ProcessPackDir, source staging, fetch or hook runs.
	var selected []config.PackEntry
	if home == os.Getenv("HOME") {
		var problems []string
		selected, problems, err = config.LoadPackEntries()
		if err != nil || len(problems) != 0 {
			return fmt.Errorf("private native current selection: %v %v", err, problems)
		}
	} else if f.userConfig == "{}" {
		// TestMain's empty-home warmup never changes process-wide HOME.
		for _, name := range packload.EmbeddedSelectedByDefault() {
			selected = append(selected, config.EmbeddedPackEntry(name))
		}
	} else {
		return fmt.Errorf("private native observational reader HOME mismatch")
	}
	resolve := func(e config.PackEntry) (*packload.Pack, error) {
		dir, known := f.packs[e.Name]
		if e.Name == "cgroup-delegate" && e.IsEmbedded && e.EmbeddedName == "cgroup-delegate" {
			dir, known = filepath.Join(repoRoot, "packs", "cgroup-delegate"), true
		}
		if !known || (e.Name != "cgroup-delegate" && e.Source != "file://"+dir) || len(e.Only) != 0 || len(e.Exclude) != 0 || e.Subdir != "" || e.Skills != nil {
			return nil, fmt.Errorf("private native unclassified selected pack: %s (%s)", e.Name, e.Source)
		}
		p, problems := packload.LoadDirForUse(dir, e.Name)
		if len(problems) != 0 || p == nil || len(p.SkewNotes) != 0 {
			return nil, fmt.Errorf("private native current manifest: %s: %v", e.Name, problems)
		}
		return p, nil
	}
	spec := config.PackSelectSpec{Resolve: resolve, FailFast: true, EmbeddedLookup: func(string) (*packload.Pack, bool) { return nil, false }}
	if home == os.Getenv("HOME") {
		spec.Selection = config.UserScopeSelection()
	}
	selection, err := config.SelectPacks(selected, spec)
	if err != nil || !selection.Complete() {
		return fmt.Errorf("private native selection closure refused: %v", err)
	}
	for _, p := range selection.Packs() {
		if p.Name == "cgroup-delegate" && (len(p.Decl.InstallContributions()) != 0 || len(p.Decl.HookContributions()) != 0) {
			return fmt.Errorf("private native default core gained executable preparation")
		}
	}
	return nil
}

func privateNativeCommandChecked(ctx context.Context, bin string, args, env []string, f *privateNativeFixture) (*exec.Cmd, error) {
	if !privateSuite.armed {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		return cmd, nil
	}
	outer := filepath.Join(privateSuite.machine, privateSuiteMarker)
	if err := privateOwnedFile(outer); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(outer)
	if err != nil || string(raw) != "private-suite-v1\n" {
		return nil, fmt.Errorf("private native outer marker invalid")
	}
	// Use the established marker/HOME adapter, but preserve the actual caller's
	// PATH (notably host-run) and all raw specialized role/TERM variables.
	routed, err := privateRouteEnvironment(env)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("private native fixture not registered")
	}
	if f.prebuiltCertFile != "" && environmentValue(routed, "SSL_CERT_FILE") != f.prebuiltCertFile {
		return nil, fmt.Errorf("private native prebuilt command does not use its owned TLS certificate")
	}
	if err := f.admit(routed); err != nil {
		return nil, err
	}
	if err := privateNativeOwnedPath(yoloBin, false); err != nil {
		return nil, err
	}
	if bin != yoloBin {
		if bin != f.copyBinary || bin == "" {
			return nil, fmt.Errorf("private native executable not the genuine CLI or its owned lifecycle copy")
		}
		if err := privateNativeOwnedPath(bin, false); err != nil {
			return nil, err
		}
		original, err := os.ReadFile(yoloBin)
		if err != nil {
			return nil, err
		}
		actual, err := os.ReadFile(bin)
		if err != nil || (!bytes.Equal(actual, original) && !bytes.Equal(actual, append(original, []byte("\nSYNTHETIC_REPLACEMENT\n")...))) {
			return nil, fmt.Errorf("private native lifecycle copy/replacement bytes drift")
		}
	}
	routed = append(routed, "PATH="+environmentValue(env, "PATH"))
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = routed
	return cmd, nil
}

func privateNativeWorkspaceAdmission(dir string) error {
	if !privateSuite.armed {
		return nil
	}
	if err := privateNativeOwnedPath(dir, true); err != nil {
		return err
	}
	for _, rel := range config.WorkspaceConfigFileNames() {
		path := filepath.Join(dir, rel)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("private native workspace discovery: %w", err)
		}
		if rel != "yolo-jail.jsonc" {
			return fmt.Errorf("private native unclassified workspace config: %s", rel)
		}
		if err := privateNativeJSON(path, "{}"); err != nil {
			return err
		}
	}
	for ancestor := filepath.Dir(dir); ; ancestor = filepath.Dir(ancestor) {
		for _, name := range config.WorkspaceConfigFileNames() {
			if _, err := os.Lstat(filepath.Join(ancestor, name)); !os.IsNotExist(err) {
				return fmt.Errorf("private native unclassified ancestor workspace input: %s", ancestor)
			}
		}
		if ancestor == filepath.Dir(ancestor) {
			break
		}
	}
	for _, rel := range []string{"mise.toml", ".mise.toml", ".tool-versions"} {
		if _, err := os.Lstat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			return fmt.Errorf("private native unclassified workspace preparation input: %s", rel)
		}
	}
	current, err := config.LoadWorkspaceConfig(dir, true, func(string) {})
	if err != nil || current == nil || len(current.Keys()) != 0 {
		return fmt.Errorf("private native current workspace selection is not empty: %v", err)
	}
	return nil
}

func environmentValue(env []string, name string) string {
	var result string
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, name+"="); ok {
			result = value
		}
	}
	return result
}

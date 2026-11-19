package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const preparationPausedNativeBuild = `mkfifo /workspace/hold; printf 'PAUSED_SYNTHETIC_CHILD\\n'; read ignored < /workspace/hold; printf 'PREPARED_TREE\n' > prepared.txt`

func preparationNativeManifest(upstream, ref, build string) string {
	manifest := map[string]any{"name": "synthetic", "contributes": []any{
		map[string]any{"kind": "provider", "name": "synthetic-provider", "api_key_env_name": "HTTP_PROXY"},
		map[string]any{"kind": "files", "into": ".synthetic/ext/example", "source": "git+file://" + upstream + "?ref=" + ref,
			"build": build, "produces": []string{"prepared.txt"}},
	}}
	data, _ := json.Marshal(manifest)
	return string(data)
}

func preparationNativeFixture(home, userConfig, pack, upstream string) *privateNativeFixture {
	return &privateNativeFixture{home: home, userConfig: userConfig, packs: map[string]string{"synthetic": pack}, check: func() error {
		entries, err := os.ReadDir(pack)
		if err != nil || len(entries) != 1 || entries[0].Name() != "pack.json" {
			return fmt.Errorf("private native scheduler unclassified pack files")
		}
		first := preparationNativeManifest(upstream, "main", "printf 'PREPARED_TREE\\n' > prepared.txt")
		paused := preparationNativeManifest(upstream, "v1.1.0", preparationPausedNativeBuild)
		if privateNativeJSON(filepath.Join(pack, "pack.json"), first) == nil {
			return privateNativeUpstream(upstream, map[string]string{"1.0.0": "ten"})
		}
		if privateNativeJSON(filepath.Join(pack, "pack.json"), paused) == nil {
			return privateNativeUpstream(upstream, map[string]string{"1.1.0": "eleven"})
		}
		return fmt.Errorf("private native scheduler source/build/program recipe drift")
	}}
}

func xbNativeFixture(home, userConfig, agent, ext, upstream string) *privateNativeFixture {
	return &privateNativeFixture{home: home, userConfig: userConfig, packs: map[string]string{xbAgent: agent, xbExt: ext}, check: func() error {
		agentFiles, extFiles := xbFixtureFiles("git+file://" + upstream + "?ref=main")
		if err := privateNativeFiles(agent, agentFiles); err != nil {
			return err
		}
		if err := privateNativeFiles(ext, extFiles); err != nil {
			return err
		}
		return privateNativeUpstream(upstream, map[string]string{"1.0.0": "ten"})
	}}
}

func xbRequesterCommand(ctx context.Context, workspace string) (*exec.Cmd, error) {
	if err := privateNativeWorkspaceAdmission(workspace); err != nil {
		return nil, err
	}
	args := append(append(jailRunArgs(), "--update"), "--", "bash", "-c", "true")
	env := append(preparationCLIEnv(os.Getenv("HOME")), paths.NoProgramReadinessEnv+"=1", "YOLO_NO_AUTO_CAPTURE=1")
	return privateNativeCommandChecked(ctx, yoloBin, args, env, privateNative)
}

// Only release's two inert text files and its locally created git database are
// admitted. The native child still uses the genuine git transport/sealed build.
func privateNativeUpstream(dir string, releases map[string]string) error {
	if err := privateNativeOwnedPath(dir, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		return fmt.Errorf("private native unclassified local source tree")
	}
	for _, entry := range entries {
		if entry.Name() != ".git" && entry.Name() != "ver.txt" && entry.Name() != "f.txt" {
			return fmt.Errorf("private native source dependency/file: %s", entry.Name())
		}
	}
	if err := privateNativeOwnedPath(filepath.Join(dir, ".git"), true); err != nil {
		return err
	}
	if err := privateNativeOwnedPath(filepath.Join(dir, ".git", "config"), false); err != nil {
		return err
	}
	for _, rel := range []string{"objects/info/alternates", "info/grafts", "refs/remotes"} {
		if _, err := os.Lstat(filepath.Join(dir, ".git", rel)); !os.IsNotExist(err) {
			return fmt.Errorf("private native unclassified git graph input: %s", rel)
		}
	}
	gitConfig, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(gitConfig), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && line != "[core]" {
			return fmt.Errorf("private native source git config contains transport/include/helper")
		}
		if line != "" && line != "[core]" && line != "repositoryformatversion = 0" && line != "filemode = true" && line != "bare = false" && line != "logallrefupdates = true" {
			return fmt.Errorf("private native source git config drift")
		}
	}
	if err := privateNativeOwnedPath(filepath.Join(dir, ".git", "hooks"), true); err != nil {
		return err
	}
	hooks, err := os.ReadDir(filepath.Join(dir, ".git", "hooks"))
	if err != nil {
		return err
	}
	for _, hook := range hooks {
		if !strings.HasSuffix(hook.Name(), ".sample") {
			return fmt.Errorf("private native source executable git hook")
		}
	}
	if err := privateNativeOwnedPath(filepath.Join(dir, "ver.txt"), false); err != nil {
		return err
	}
	version, err := os.ReadFile(filepath.Join(dir, "ver.txt"))
	if err != nil {
		return err
	}
	ten, known := releases[strings.TrimSuffix(string(version), "\n")]
	if !known {
		return fmt.Errorf("private native unknown synthetic local release")
	}
	var body strings.Builder
	for i := 1; i <= 30; i++ {
		if i == 10 {
			body.WriteString(ten + "\n")
		} else {
			body.WriteString(strings.Repeat("x", i) + "\n")
		}
	}
	if err := privateNativeOwnedPath(filepath.Join(dir, "f.txt"), false); err != nil {
		return err
	}
	actual, err := os.ReadFile(filepath.Join(dir, "f.txt"))
	if err != nil || string(actual) != body.String() {
		return fmt.Errorf("private native synthetic source content drift")
	}
	return nil
}

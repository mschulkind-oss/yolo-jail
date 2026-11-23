package integration

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

type privateNativeForkPin struct {
	repo, source, key, forkDir, baseDir string
}

func privateNativeForkGit(repo string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	cmd.Env = testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ()))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("private native local fork observation: %w", err)
	}
	return out, nil
}

func (pin *privateNativeForkPin) commits() (map[string]bool, error) {
	if err := privateNativeDirectories(map[string][]string{pin.repo: {".git", "rev.txt"}}); err != nil {
		return nil, err
	}
	if err := privateNativeOwnedPath(filepath.Join(pin.repo, "rev.txt"), false); err != nil {
		return nil, err
	}
	current, err := os.ReadFile(filepath.Join(pin.repo, "rev.txt"))
	if err != nil || (string(current) != "1\n" && string(current) != "1b\n" && string(current) != "2\n") {
		return nil, fmt.Errorf("private native plain fork revision is unclassified")
	}
	gitDir := filepath.Join(pin.repo, ".git")
	if err := privateNativeOwnedPath(gitDir, true); err != nil {
		return nil, err
	}
	if err := privateNativeOwnedPath(filepath.Join(gitDir, "config"), false); err != nil {
		return nil, err
	}
	configBytes, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		return nil, err
	}
	// The lines `git init` itself writes. On darwin it adds ignorecase and precomposeunicode
	// when the volume is case-insensitive (APFS's default), so those are git's own defaults too.
	initLines := map[string]bool{
		"": true, "[core]": true, "repositoryformatversion = 0": true, "filemode = true": true,
		"bare = false": true, "logallrefupdates = true": true,
		"ignorecase = true": true, "precomposeunicode = true": true,
	}
	for _, line := range strings.Split(string(configBytes), "\n") {
		if !initLines[strings.TrimSpace(line)] {
			return nil, fmt.Errorf("private native plain fork git config is unclassified")
		}
	}
	for _, rel := range []string{"objects/info/alternates", "info/grafts", "refs/remotes", "shallow"} {
		if _, err := os.Lstat(filepath.Join(gitDir, rel)); !os.IsNotExist(err) {
			return nil, fmt.Errorf("private native plain fork git graph drift: %s", rel)
		}
	}
	if err := privateNativeOwnedPath(filepath.Join(gitDir, "hooks"), true); err != nil {
		return nil, err
	}
	hooks, err := os.ReadDir(filepath.Join(gitDir, "hooks"))
	if err != nil {
		return nil, err
	}
	for _, hook := range hooks {
		if !strings.HasSuffix(hook.Name(), ".sample") {
			return nil, fmt.Errorf("private native plain fork active git hook")
		}
	}
	head, err := privateNativeForkGit(pin.repo, "symbolic-ref", "HEAD")
	if err != nil || string(head) != "refs/heads/main\n" {
		return nil, fmt.Errorf("private native plain fork branch is not main")
	}
	log, err := privateNativeForkGit(pin.repo, "log", "--format=%H", "main")
	if err != nil {
		return nil, err
	}
	commits := map[string]bool{}
	revisions := map[string]bool{}
	for _, commit := range strings.Fields(string(log)) {
		if _, err := hex.DecodeString(commit); err != nil || len(commit) != 40 {
			return nil, fmt.Errorf("private native plain fork commit malformed")
		}
		files, err := privateNativeForkGit(pin.repo, "ls-tree", "--name-only", "-z", commit)
		if err != nil || string(files) != "rev.txt\x00" {
			return nil, fmt.Errorf("private native plain fork commit contains unknown inputs")
		}
		revision, err := privateNativeForkGit(pin.repo, "show", commit+":rev.txt")
		if err != nil || (string(revision) != "1\n" && string(revision) != "1b\n" && string(revision) != "2\n") || revisions[string(revision)] {
			return nil, fmt.Errorf("private native plain fork commit is not a unique planned revision")
		}
		revisions[string(revision)], commits[commit] = true, true
	}
	if len(commits) == 0 || len(commits) > 3 || !revisions["1\n"] {
		return nil, fmt.Errorf("private native plain fork history is unclassified")
	}
	return commits, nil
}

func (pin *privateNativeForkPin) generatedInput(name string) bool {
	return name == packsrc.ForkLockName || name == packsrc.LockName || name == packsrc.IdentityIndexName || name == "."+packsrc.IdentityIndexName+".lock"
}

func (pin *privateNativeForkPin) admitPackLock(configPath string) (bool, error) {
	path := packsrc.LockPath(configPath)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := privateNativeOwnedPath(path, false); err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0644 {
		return false, fmt.Errorf("private native plain fork local pack lock mode drift")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var strict packsrc.Lock
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&strict); err != nil || strict.Schema != packsrc.LockSchema || len(strict.Packs) != 2 {
		return false, fmt.Errorf("private native plain fork local pack lock schema/shape drift")
	}
	lock, err := packsrc.LoadLock(path)
	if err != nil || lock.Schema != packsrc.LockSchema || len(lock.Packs) != 2 {
		return false, fmt.Errorf("private native plain fork local pack lock normal loader refused")
	}
	for name, root := range map[string]string{"forkfixture-base": pin.baseDir, "forkfixture-fork": pin.forkDir} {
		entry, ok := lock.Packs[name]
		if !ok || entry.Name != name || entry.Source != "file://"+root || entry.Commit != "" || entry.Ref != "" {
			return false, fmt.Errorf("private native plain fork local pack record identity drift")
		}
	}
	return true, nil
}

func (pin *privateNativeForkPin) admitIndex(configPath string) error {
	localRecords, err := pin.admitPackLock(configPath)
	if err != nil {
		return err
	}
	path := packsrc.IdentityIndexPath(configPath)
	lockPath := filepath.Join(filepath.Dir(path), "."+packsrc.IdentityIndexName+".lock")
	_, indexErr := os.Lstat(path)
	_, lockErr := os.Lstat(lockPath)
	if os.IsNotExist(indexErr) && os.IsNotExist(lockErr) {
		return nil
	}
	if indexErr != nil || lockErr != nil {
		return fmt.Errorf("private native plain fork identity bookkeeping incomplete")
	}
	for file, mode := range map[string]os.FileMode{path: 0600, lockPath: 0644} {
		if err := privateNativeOwnedPath(file, false); err != nil {
			return err
		}
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != mode || (file == lockPath && info.Size() != 0) {
			return fmt.Errorf("private native plain fork identity bookkeeping mode/content drift")
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var strict packsrc.IdentityIndex
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&strict); err != nil || strict.Schema != packsrc.IdentityIndexSchema || len(strict.Packs) < 1 || len(strict.Packs) > 2 {
		return fmt.Errorf("private native plain fork identity index schema/shape drift")
	}
	index, err := packsrc.LoadIdentityIndex(path)
	identity := packsrc.PackIdentity("", pin.forkDir, "")
	if err != nil || identity == "" || index.Schema != packsrc.IdentityIndexSchema || len(index.Packs) < 1 || len(index.Packs) > 2 || index.Packs[identity] != "forkfixture-fork" {
		return fmt.Errorf("private native plain fork identity index does not bind its actual owned source")
	}
	for id, handle := range index.Packs {
		if id == identity && handle == "forkfixture-fork" {
			continue
		}
		if !localRecords || id != packsrc.PackIdentity("", pin.baseDir, "") || handle != "forkfixture-base" {
			return fmt.Errorf("private native plain fork identity index has an unclassified adoption")
		}
	}
	return nil
}

func (pin *privateNativeForkPin) admit(configPath string) error {
	if err := pin.admitIndex(configPath); err != nil {
		return err
	}
	commits, err := pin.commits()
	if err != nil {
		return err
	}
	path := packsrc.ForkLockPath(configPath)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := privateNativeOwnedPath(path, false); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0644 {
		return fmt.Errorf("private native generated fork pin mode drift")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var strict packsrc.ForkLock
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&strict); err != nil || strict.Schema != packsrc.ForkLockSchema || len(strict.Forks) != 1 {
		return fmt.Errorf("private native generated fork pin shape/schema drift")
	}
	lock, err := packsrc.LoadForkLock(path)
	if err != nil || lock.Schema != packsrc.ForkLockSchema || len(lock.Forks) != 1 {
		return fmt.Errorf("private native generated fork pin normal loader refused")
	}
	entry, ok := lock.Forks[pin.key]
	if !ok || entry.Key != pin.key || entry.Source != pin.source || entry.Ref != "main" || !commits[entry.Commit] {
		return fmt.Errorf("private native generated fork pin identity drift")
	}
	return nil
}

func plainForkNativeFixture(home, base, fork, repo string) *privateNativeFixture {
	baseManifest := `{"name":"forkfixture-base","contributes":[{"kind":"program","bin":"forkfixture","via":"installer","url":"file:///ctx/packs/forkfixture-base/install.sh"}]}`
	installer := "#!/bin/bash\nmkdir -p \"$HOME/.local/bin\"\nprintf '#!/bin/bash\\necho FORKFIXTURE_BASE_INSTALLER_RAN\\n' > \"$HOME/.local/bin/forkfixture\"\nchmod +x \"$HOME/.local/bin/forkfixture\"\n"
	build := `mkdir -p "$HOME/.local/bin" && printf '#!/bin/sh\necho FORKFIXTURE_FORK_BUILD_%s\n' "$(cat rev.txt)" > "$HOME/.local/bin/forkfixture" && chmod +x "$HOME/.local/bin/forkfixture"`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	source := "git+file://" + repo + "?ref=main"
	forkManifest := `{"name":"forkfixture-fork","contributes":[{"kind":"program","bin":"forkfixture","via":"source","fork_of":"forkfixture-base","source":"` + source + `","build":"` + buildJSON + `","produces":[".local/bin/forkfixture"]}]}`
	cfg := `{"packs": [{"source": "file://` + base + `", "name": "forkfixture-base"}, {"source": "file://` + fork + `", "name": "forkfixture-fork"}]}`
	pin := &privateNativeForkPin{repo: repo, source: source, key: "forkfixture-fork/forkfixture", forkDir: fork, baseDir: base}
	return &privateNativeFixture{home: home, userConfig: cfg, packs: map[string]string{"forkfixture-base": base, "forkfixture-fork": fork}, forkPin: pin, check: func() error {
		if err := privateNativeJSON(filepath.Join(base, "pack.json"), baseManifest); err != nil {
			return err
		}
		if err := privateNativeFiles(base, map[string]string{"pack.json": baseManifest, "install.sh": installer}); err != nil {
			return err
		}
		if err := privateNativeJSON(filepath.Join(fork, "pack.json"), forkManifest); err != nil {
			return err
		}
		return privateNativeFiles(fork, map[string]string{"pack.json": forkManifest})
	}}
}

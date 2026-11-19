package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

const privateSuiteMarker = ".private-integration-suite"

var privateSuite struct {
	armed                               bool
	machine, root, driver, prefixDriver string
	driverDigest, prefixDigest          [32]byte
}

// The suite is armed only by the receipt wrapper's explicit private machine HOME.
// Once admitted, an absent later marker is an error, never ordinary execution.
func armPrivateSuite() (func(), error) {
	machine := os.Getenv("HOME")
	marker := filepath.Join(machine, privateSuiteMarker)
	if _, err := os.Lstat(marker); os.IsNotExist(err) {
		return func() {}, nil
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("private suite adapter unsupported outside Linux; run native platform verification separately")
	}
	if filepath.Dir(machine) != filepath.Join(repoRoot, ".yolo", "durable", "toml-primary-integration", "safety-continuation") || !strings.HasPrefix(filepath.Base(machine), "machine-") {
		return nil, fmt.Errorf("private suite admission requires its receipt wrapper's owned machine HOME, not owner/live HOME")
	}
	if err := privateOwnedFile(marker); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "private-suite-v1\n" {
		return nil, fmt.Errorf("invalid private suite admission marker")
	}
	root, err := os.MkdirTemp("", "yolo-private-suite-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { privateSuite.armed = false; _ = os.RemoveAll(root) }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	driver, err := buildPrivateSuiteDriver(ctx, root)
	if err != nil {
		cleanup()
		return nil, err
	}
	bytes, err := os.ReadFile(driver)
	if err != nil {
		cleanup()
		return nil, err
	}
	privateSuite.armed, privateSuite.machine, privateSuite.root, privateSuite.driver = true, machine, root, driver
	privateSuite.driverDigest = sha256.Sum256(bytes)
	return cleanup, nil
}

func privateOwnedFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return fmt.Errorf("private routing file is missing, linked or has invalid mode: %s", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("private routing file is not owned by this test process: %s", path)
	}
	return nil
}

func buildPrivateSuiteDriver(ctx context.Context, root string) (string, error) {
	bin := filepath.Join(root, "bin", "yolo-private-official")
	if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil {
		return "", err
	}
	share := filepath.Join(root, "share")
	if err := os.MkdirAll(share, 0755); err != nil {
		return "", err
	}
	if err := os.Symlink(bundleRoot, filepath.Join(share, "yolo-jail")); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(bundleRoot, "yolo-build-stamp.json"))
	if err != nil {
		return "", err
	}
	var stamp struct{ Version, Commit, SnapshotIdentity, BuildKind string }
	if err := json.Unmarshal(raw, &stamp); err != nil {
		return "", err
	}
	var flags []string
	for name, value := range map[string]string{"buildVersion": stamp.Version, "GitCommit": stamp.Commit, "SnapshotIdentity": stamp.SnapshotIdentity, "BuildKind": stamp.BuildKind} {
		flags = append(flags, "-X", "github.com/mschulkind-oss/yolo-jail/internal/version."+name+"="+value)
	}
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-ldflags", strings.Join(flags, " "), "-o", bin, "./internal/cli")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("compile private suite driver: %w\n%s", err, out)
	}
	return bin, nil
}

func seedPrivateSuiteHome(home string) error {
	if !privateSuite.armed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp(home, ".private-registry-")
	if err != nil {
		return err
	}
	ref := imageExists("podman")
	if ref == "" {
		return fmt.Errorf("private suite needs the loaded matched image")
	}
	probe := exec.CommandContext(ctx, "podman", "run", "--rm", "--pull=never", "--network=none", "--read-only", "--userns=host", "--cgroups=disabled", "--entrypoint=/bin/bash", ref, "-c", `set -eu; node -p process.execPath; readlink -f "$(command -v npm)"; cat /etc/yolo-jail-image-identity`)
	out, err := probe.Output()
	if err != nil {
		return err
	}
	values := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(values) != 3 {
		return fmt.Errorf("private suite runtime observation malformed")
	}
	cmd := exec.CommandContext(ctx, privateSuite.driver, append([]string{"private-official-setup", root}, values...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("private registry setup: %w\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "home-binding"), []byte(home), 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, ".private-official-producer"), []byte(root), 0600)
}

func privateSuiteHomeBinding(root, home string) error {
	if err := privateOwnedFile(filepath.Join(root, "home-binding")); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(root, "home-binding"))
	if err != nil || string(raw) != home {
		return fmt.Errorf("private registry active HOME mismatch")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return fmt.Errorf("private registry not a private regular directory")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return fmt.Errorf("private registry has linked ancestry")
	}
	return nil
}

func privateLaunchEnvironment(t *testing.T, cfg runConfig) []string {
	t.Helper()
	env, err := privateRouteEnvironment(launchEnvironment(cfg))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func privateRouteEnvironment(env []string) ([]string, error) {
	if !privateSuite.armed {
		return env, nil
	}
	home := launchHome(env)
	marker := filepath.Join(home, ".private-official-producer")
	if err := privateOwnedFile(marker); err != nil {
		return nil, fmt.Errorf("private suite active HOME adapter missing: %w", err)
	}
	raw, err := os.ReadFile(marker)
	root := string(raw)
	if err != nil || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("private suite marker is invalid")
	}
	if err := privateSuiteHomeBinding(root, home); err != nil {
		return nil, fmt.Errorf("private suite registry binding: %w", err)
	}
	path := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			path = strings.TrimPrefix(entry, "PATH=")
		}
	}
	env = append(env, "PATH="+filepath.Join(root, "tools")+":"+path, "NODE_OPTIONS=")
	for _, pair := range []struct{ key, subdir string }{
		{"XDG_CONFIG_HOME", ".config"}, {"XDG_DATA_HOME", ".local/share"},
		{"XDG_CACHE_HOME", ".cache"}, {"XDG_RUNTIME_DIR", ".run"},
	} {
		env = append(env, pair.key+"="+filepath.Join(home, pair.subdir))
	}
	return env, nil
}

func privateLaunchCommand(t *testing.T, cfg runConfig, bin string, args []string) (string, []string) {
	t.Helper()
	name, argv, err := privateLaunchCommandChecked(cfg, bin, args)
	if err != nil {
		t.Fatal(err)
	}
	return name, argv
}

func privateLaunchCommandChecked(cfg runConfig, bin string, args []string) (string, []string, error) {
	ordinary := func() (string, []string, error) {
		name, argv := launchCommand(cfg, bin, args)
		return name, argv, nil
	}
	if !privateSuite.armed {
		return ordinary()
	}
	outer := filepath.Join(privateSuite.machine, privateSuiteMarker)
	if err := privateOwnedFile(outer); err != nil {
		return "", nil, fmt.Errorf("private suite admission lost: %w", err)
	}
	outerValue, err := os.ReadFile(outer)
	if err != nil || string(outerValue) != "private-suite-v1\n" {
		return "", nil, fmt.Errorf("private suite outer admission marker changed")
	}
	home := launchHome(launchEnvironment(cfg))
	marker := filepath.Join(home, ".private-official-producer")
	if err := privateOwnedFile(marker); err != nil {
		return "", nil, fmt.Errorf("private suite active HOME adapter missing: %w", err)
	}
	raw, err := os.ReadFile(marker)
	root := string(raw)
	if err != nil || !filepath.IsAbs(root) {
		return "", nil, fmt.Errorf("private suite marker is invalid")
	}
	if err := privateSuiteHomeBinding(root, home); err != nil {
		return "", nil, fmt.Errorf("private suite registry binding: %w", err)
	}
	if bin != yoloBin {
		return "", nil, fmt.Errorf("private suite command override unsupported; no ordinary CLI fallback")
	}
	if privateNative != nil || (len(args) == 2 && args[0] == "--version" && args[1] == "--full") {
		if len(cfg.prefix) != 0 {
			return "", nil, fmt.Errorf("private native route cannot substitute a driver prefix")
		}
		fixture := privateNative
		if fixture == nil {
			fixture = &privateNativeFixture{home: home, userConfig: "{}"}
		}
		cmd, err := privateNativeCommandChecked(context.Background(), bin, args, launchEnvironment(cfg), fixture)
		if err != nil {
			return "", nil, err
		}
		return cmd.Path, cmd.Args[1:], nil
	}
	if len(cfg.prefix) != 0 {
		if len(cfg.prefix) != 2 || cfg.prefix[1] != "private-official-cli" || cfg.prefix[0] != privateSuite.prefixDriver {
			return "", nil, fmt.Errorf("private suite unknown launcher prefix unsupported")
		}
		if err := privateDriverIdentity(cfg.prefix[0], privateSuite.prefixDigest); err != nil {
			return "", nil, err
		}
		return ordinary()
	}
	if err := privateDriverIdentity(privateSuite.driver, privateSuite.driverDigest); err != nil {
		return "", nil, err
	}
	return privateSuite.driver, append([]string{"private-official-cli", bin}, args...), nil
}

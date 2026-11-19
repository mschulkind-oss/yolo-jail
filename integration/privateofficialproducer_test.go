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
	"testing"
	"time"
)

// privateOfficialMachineHome must run before a test's later packHome calls: even
// their supplied machine home is private, rather than a link to the owner's stores.
func privateOfficialMachineHome(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("private physical-image producer adapter unsupported outside Linux; native Mac verification remains required")
	}
	previous := hostHome
	hostHome = resolvedTempDir(t)
	t.Cleanup(func() { hostHome = previous })
	t.Setenv("HOME", hostHome)
	for _, item := range []struct{ key, subdir string }{
		{"XDG_CONFIG_HOME", ".config"}, {"XDG_DATA_HOME", ".local/share"},
		{"XDG_CACHE_HOME", ".cache"}, {"XDG_RUNTIME_DIR", ".run"},
	} {
		t.Setenv(item.key, filepath.Join(hostHome, item.subdir))
	}
}

// withPrivateOfficialProducer substitutes only the host producer's outer runtime
// and registry commands. It executes cli.Main in a separate test binary, whose
// self-exec preparation workers retain the real argument and environment paths.
// The guest still uses the actual matched bundle, unchanged packs and assertions.
func withPrivateOfficialProducer(t *testing.T) runOption {
	t.Helper()
	root := resolvedTempDir(t)
	bin := filepath.Join(root, "bin", "yolo-private-official")
	if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil {
		t.Fatal(err)
	}
	share := filepath.Join(root, "share")
	if err := os.MkdirAll(share, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bundleRoot, filepath.Join(share, "yolo-jail")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	stampRaw, err := os.ReadFile(filepath.Join(bundleRoot, "yolo-build-stamp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stamp struct{ Version, Commit, SnapshotIdentity, BuildKind string }
	if err := json.Unmarshal(stampRaw, &stamp); err != nil {
		t.Fatal(err)
	}
	var ldflags []string
	for name, value := range map[string]string{
		"buildVersion": stamp.Version, "GitCommit": stamp.Commit,
		"SnapshotIdentity": stamp.SnapshotIdentity, "BuildKind": stamp.BuildKind,
	} {
		ldflags = append(ldflags, "-X", "github.com/mschulkind-oss/yolo-jail/internal/version."+name+"="+value)
	}
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-ldflags", strings.Join(ldflags, " "), "-o", bin, "./internal/cli")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile private producer driver: %v\n%s", err, out)
	}
	if privateSuite.armed {
		raw, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}
		previousPath, previousDigest := privateSuite.prefixDriver, privateSuite.prefixDigest
		privateSuite.prefixDriver, privateSuite.prefixDigest = bin, sha256.Sum256(raw)
		t.Cleanup(func() { privateSuite.prefixDriver, privateSuite.prefixDigest = previousPath, previousDigest })
	}
	// Observe, never invent, the actual image's runtime identity. This probe has
	// no network, writable root, package installation or agent invocation.
	ref := imageExists("podman")
	if ref == "" {
		t.Fatal("private producer needs the loaded test image; run the image setup first")
	}
	probe := exec.CommandContext(ctx, "podman", "run", "--rm", "--pull=never", "--network=none", "--read-only", "--userns=host", "--cgroups=disabled", "--entrypoint=/bin/bash", ref, "-c",
		`set -eu; node -p process.execPath; readlink -f "$(command -v npm)"; cat /etc/yolo-jail-image-identity`)
	observed, err := probe.Output()
	if err != nil {
		t.Fatalf("observe actual private-producer runtime: %v", err)
	}
	values := strings.Split(strings.TrimSpace(string(observed)), "\n")
	if len(values) != 3 {
		t.Fatalf("actual runtime probe returned malformed observations: %q", observed)
	}
	cmd = exec.CommandContext(ctx, bin, append([]string{"private-official-setup", root}, values...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("initialize private producer: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".private-official-producer"), []byte(root), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "home-binding"), []byte(os.Getenv("HOME")), 0600); err != nil {
		t.Fatal(err)
	}
	// Config moves with packHome; use that active private home, not its machine
	// store home, for XDG config. No owner configuration enters this driver.
	for _, item := range []struct{ key, subdir string }{
		{"XDG_CONFIG_HOME", ".config"}, {"XDG_DATA_HOME", ".local/share"},
		{"XDG_CACHE_HOME", ".cache"}, {"XDG_RUNTIME_DIR", ".run"},
	} {
		t.Setenv(item.key, filepath.Join(os.Getenv("HOME"), item.subdir))
	}
	t.Setenv("PATH", filepath.Join(root, "tools")+":"+os.Getenv("PATH"))
	t.Setenv("NODE_OPTIONS", "")
	t.Cleanup(func() {
		events, err := os.ReadFile(filepath.Join(root, "private-official-events"))
		if err != nil || string(events) != "install\nci\ninstall\n" {
			t.Errorf("real CLI cold/fresh preparation events = %q, %v; want two resolutions and one immutable install", events, err)
		}
		configs, err := os.ReadFile(filepath.Join(root, "npm-userconfigs"))
		if err != nil {
			t.Errorf("private producer config observer: %v", err)
			return
		}
		for _, path := range strings.Split(strings.TrimSpace(string(configs)), "\n") {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Errorf("private npm userconfig survived: %s (%v)", path, err)
			}
		}
	})
	return withLauncherPrefix(bin, "private-official-cli")
}

func TestPrivateOfficialProducerWrapperPreservesMatchedLauncherAndArgs(t *testing.T) {
	cfg := runConfig{}
	withLauncherPrefix("driver", "private-official-cli")(&cfg)
	name, argv := launchCommand(cfg, "/matched/yolo", []string{"run", "--", "true"})
	if name != "driver" || fmt.Sprint(argv) != "[private-official-cli /matched/yolo run -- true]" {
		t.Fatalf("private producer wrapper argv = %q %v", name, argv)
	}
}

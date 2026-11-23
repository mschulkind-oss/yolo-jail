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
//
// PRIVATE MEANS YOLO'S STORE, NOT THE CONTAINER RUNTIME'S. The new machine home keeps
// its own yolo state, but re-links every other packHomeSharedStores entry (podman's
// store and config, nix's cache and config) to the machine home it replaces, and its
// .run to the runtime directory it inherited. A bare home here passed in yolo's own
// jail, where podman is root and its graphroot is /var/lib/containers/storage whatever
// HOME says, and failed on every GitHub runner: rootless podman keeps its graphroot at
// $XDG_DATA_HOME/containers/storage, so the redirected XDG_DATA_HOME showed it an empty
// store and withPrivateOfficialProducer found no loaded image (CI run 44565187482).
func privateOfficialMachineHome(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("private physical-image producer adapter unsupported outside Linux; native Mac verification remains required")
	}
	previous := hostHome
	machine := previous
	if machine == "" {
		machine = os.Getenv("HOME")
	}
	home := resolvedTempDir(t)
	if err := linkMachineToolStores(home, machine); err != nil {
		t.Fatal(err)
	}
	hostHome = home
	t.Cleanup(func() { hostHome = previous })
	t.Setenv("HOME", hostHome)
	runtimeDir := privateRuntimeDir(t, hostHome)
	for _, item := range []struct{ key, subdir string }{
		{"XDG_CONFIG_HOME", ".config"}, {"XDG_DATA_HOME", ".local/share"},
		{"XDG_CACHE_HOME", ".cache"},
	} {
		t.Setenv(item.key, filepath.Join(hostHome, item.subdir))
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
}

// linkMachineToolStores links every packHomeSharedStores entry except yolo's own store
// from home to the same entry under machine, creating each target first (MkdirAll
// refuses a dangling link; see isolateHome). Yolo's store is left absent, so the next
// seedPackHome against home creates it private.
func linkMachineToolStores(home, machine string) error {
	for _, store := range packHomeSharedStores {
		rel := filepath.FromSlash(store)
		if rel == yoloStoreRel {
			continue
		}
		target := filepath.Join(machine, rel)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("creating machine store %s: %w", target, err)
		}
		link := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return err
		}
		if err := userSymlink(target, link); err != nil {
			return err
		}
	}
	return nil
}

// privateRuntimeDir returns home/.run, the only XDG_RUNTIME_DIR the private suite admits
// besides an empty one (privateNativeFixture.admit), made a link to the runtime directory
// this process inherited. Rootless podman keeps its pause process, its libpod tmp dir and
// its systemd user-bus socket there, so an empty directory under that name would show it
// none of them. With no existing inherited runtime directory (root podman, as in yolo's
// own jail) nothing is linked and the bare path is returned, as before.
func privateRuntimeDir(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".run")
	inherited := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(inherited) || inherited == dir {
		return dir
	}
	// A link to a missing directory dangles, and MkdirAll refuses one (isolateHome).
	if info, err := os.Stat(inherited); err != nil || !info.IsDir() {
		return dir
	}
	if _, err := os.Lstat(dir); err == nil {
		return dir
	}
	if err := userSymlink(inherited, dir); err != nil {
		t.Fatal(err)
	}
	return dir
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
	runtimeDir := privateRuntimeDir(t, os.Getenv("HOME"))
	for _, item := range []struct{ key, subdir string }{
		{"XDG_CONFIG_HOME", ".config"}, {"XDG_DATA_HOME", ".local/share"},
		{"XDG_CACHE_HOME", ".cache"},
	} {
		t.Setenv(item.key, filepath.Join(os.Getenv("HOME"), item.subdir))
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
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

// TestPrivateOfficialMachineHomeKeepsTheRuntimesStores pins privateOfficialMachineHome
// under -short: its yolo store is private, but podman's and nix's stores, as seen through
// both HOME and the XDG variables it sets, are the replaced machine home's, and its
// runtime directory is the inherited one. A bare home passes in yolo's own jail (root
// podman) and leaves rootless podman an empty image store on every GitHub runner.
func TestPrivateOfficialMachineHomeKeepsTheRuntimesStores(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("privateOfficialMachineHome is Linux-only")
	}
	machine := resolvedTempDir(t)
	inheritedRuntime := resolvedTempDir(t)
	saved := hostHome
	hostHome = machine
	t.Cleanup(func() { hostHome = saved })
	t.Setenv("HOME", machine)
	t.Setenv("XDG_RUNTIME_DIR", inheritedRuntime)

	privateOfficialMachineHome(t)

	home := os.Getenv("HOME")
	if home == machine || hostHome != home {
		t.Fatalf("HOME=%s hostHome=%s; want a new machine home replacing %s", home, hostHome, machine)
	}
	resolve := func(path string) string {
		t.Helper()
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatalf("%s does not resolve: %v", path, err)
		}
		return real
	}
	for _, store := range packHomeSharedStores {
		rel := filepath.FromSlash(store)
		if rel == yoloStoreRel {
			if _, err := os.Lstat(filepath.Join(home, rel)); !os.IsNotExist(err) {
				t.Errorf("the private machine home's yolo store is not left private: %v", err)
			}
			continue
		}
		if got, want := resolve(filepath.Join(home, rel)), filepath.Join(machine, rel); got != want {
			t.Errorf("%s resolves to %s, want the machine's %s", rel, got, want)
		}
	}
	// Rootless podman finds its graphroot and its config through the XDG variables.
	if got, want := resolve(filepath.Join(os.Getenv("XDG_DATA_HOME"), "containers")), filepath.Join(machine, ".local", "share", "containers"); got != want {
		t.Errorf("$XDG_DATA_HOME/containers resolves to %s, want %s", got, want)
	}
	if got, want := resolve(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "containers")), filepath.Join(machine, ".config", "containers"); got != want {
		t.Errorf("$XDG_CONFIG_HOME/containers resolves to %s, want %s", got, want)
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if want := filepath.Join(home, ".run"); runtimeDir != want {
		t.Errorf("XDG_RUNTIME_DIR=%s, want the private suite's admitted %s", runtimeDir, want)
	}
	if got := resolve(runtimeDir); got != inheritedRuntime {
		t.Errorf("XDG_RUNTIME_DIR resolves to %s, want the inherited %s", got, inheritedRuntime)
	}
}

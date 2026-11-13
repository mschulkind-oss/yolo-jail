package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

type selectedNPMFixture struct {
	store              *capture.Store
	in                 npmCaptureInput
	root, tools, node  string
	resolves, installs atomic.Int32
}

func init() {
	if len(os.Args) > 5 && os.Args[1] == "npm-capture-runtime-test" && os.Args[3] == "internal" && os.Args[4] == programCaptureJailVerb {
		npmCaptureImagePath = filepath.Join(os.Args[2], "image")
		npmCaptureNode = npmCaptureNodeWithSyntheticTrust
		// A fixture that stands in an image /bin (image-bins under its root) points the sealed
		// producer's image probe at it; without one the probe keeps the image's real dirs.
		if bins := filepath.Join(os.Args[2], "image-bins"); isDir(bins) {
			entrypoint.OverrideImageProbeBase(bins)
		}
		captureRunPipeline = func(o run.Options) int {
			// Actual seal caller and owned child; only the outer container is substituted.
			if !o.Sealed || o.OnlyPacks == nil || len(o.OnlyPacks) != 0 || !o.NeverAttach || !o.NoProgramReadiness || o.CapturesDir() != "" || o.OnRuntimeResolved == nil {
				fmt.Fprintln(os.Stderr, "synthetic runtime received unsealed capture options")
				return 1
			}
			if err := o.OnRuntimeResolved("podman"); err != nil {
				return 1
			}
			home, err := os.MkdirTemp("", "npm-private-runtime-")
			if err != nil {
				return 1
			}
			defer os.RemoveAll(home)
			previousHome, previousSeal := os.Getenv("HOME"), os.Getenv(entrypoint.SealedBuildEnv)
			_ = os.Setenv("HOME", home)
			_ = os.Setenv(entrypoint.SealedBuildEnv, "1")
			defer func() { _ = os.Setenv("HOME", previousHome); _ = os.Setenv(entrypoint.SealedBuildEnv, previousSeal) }()
			argv := append([]string(nil), o.Args[2:]...)
			for i, a := range argv {
				argv[i] = strings.ReplaceAll(a, "/workspace", o.Workspace)
			}
			return runInternal(argv)
		}
		os.Exit(runInternal(os.Args[4:]))
	}
	if len(os.Args) > 5 && os.Args[1] == "npm-capture-test-image" && os.Args[3] == "internal" &&
		(os.Args[4] == "program-npm" || os.Args[4] == "capture-materialize" || os.Args[4] == "log-append") {
		npmCaptureImagePath = os.Args[2]
		npmCaptureNode = npmCaptureNodeWithSyntheticTrust
		os.Exit(runInternal(os.Args[4:]))
	}
}

// The host-executed producer fixture owns its own CA. It runs the same trust validator,
// Node resolver, PATH lookup and commands as production, without depending on an image-only path.
func npmCaptureNodeWithSyntheticTrust(ctx context.Context, home, floor, userConfig string, w io.Writer) (string, error) {
	return npmCaptureNodeWithIO(ctx, home, floor, userConfig, w, npmCaptureNodeIO{
		trustStorePath:   filepath.Join(filepath.Dir(npmCaptureImagePath), "synthetic-ca.pem"),
		validateTrust:    validateNPMCaptureTrustStore,
		resolveNodeFloor: entrypoint.ResolveNodeForFloor,
		lookPath:         exec.LookPath,
		runCommand: func(ctx context.Context, name string, args, env []string, stdout, stderr io.Writer) error {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env, cmd.Stdout, cmd.Stderr = env, stdout, stderr
			return cmd.Run()
		},
	})
}

func newSelectedNPMFixture(t *testing.T) *selectedNPMFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux artifact ownership")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("local Node runtime fixture unavailable")
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		t.Fatal(err)
	}
	f := &selectedNPMFixture{store: &capture.Store{Dir: t.TempDir()}, in: npmCaptureInput{Owner: "fixture", Bin: "probe", Package: "probe-pkg"}, root: t.TempDir(), tools: t.TempDir(), node: node}
	t.Setenv("HOME", t.TempDir())
	// THE PRODUCER CHILD'S TEMP FILES ARE THE TEST'S. The synthetic runtime runs the in-jail
	// producer on this machine, where a real one runs in a sealed jail whose /tmp dies with it;
	// a cancellation test stops that child with SIGINT and then SIGKILL (runCaptureChild), so its
	// own removal of its npm config area and runtime home never runs. runCaptureChild passes
	// TMPDIR through, so they land here and go with the test.
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("PATH", f.tools+":"+os.Getenv("PATH"))
	t.Setenv("NODE_OPTIONS", "")
	binary, err := os.ReadFile(f.node)
	if err != nil {
		t.Fatal(err)
	}
	f.node = filepath.Join(f.tools, "node")
	if err := os.WriteFile(f.node, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NPM_TOKEN", "must-not-cross")
	t.Setenv("SYNTHETIC_PROVIDER_KEY", "must-not-cross")
	oldImage := npmCaptureImagePath
	npmCaptureImagePath = filepath.Join(f.root, "image")
	t.Cleanup(func() { npmCaptureImagePath = oldImage })
	if err = os.WriteFile(npmCaptureImagePath, []byte("private-synthetic-runtime-v1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.root, "synthetic-ca.pem"), []byte("synthetic fixture CA"), 0600); err != nil {
		t.Fatal(err)
	}
	f.version(t, "1.0.0")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := "#!/bin/sh\nexec " + shquote.Quote(self) + " npm-capture-test-image " + shquote.Quote(npmCaptureImagePath) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(f.tools, "yolo"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/bash
set -eu
ROOT=` + shquote.Quote(f.root) + `
if [ -n "${NPM_CONFIG_USERCONFIG:-}" ]; then
 [ "$NPM_CONFIG_USERCONFIG" != "$NPM_CONFIG_GLOBALCONFIG" ] && [ -f "$NPM_CONFIG_USERCONFIG" ] && [ ! -s "$NPM_CONFIG_USERCONFIG" ]
 case "$NPM_CONFIG_USERCONFIG" in /*) ;; *) exit 34;; esac
 printf '%s\n' "$NPM_CONFIG_USERCONFIG" >> "$ROOT/npm-userconfigs"
 [ "$NPM_CONFIG_GLOBALCONFIG" = /dev/null ]
fi
if [ "$1" = --version ]; then echo 10.0.0; exit 0; fi
phase=$1; shift
if [ "$phase" = view ]; then
 printf 'legacy-view\n' >> "$ROOT/counts"
 [ ! -f "$ROOT/legacy-fail" ] || exit 39
 cat "$ROOT/version"; exit 0
fi
if [ "$phase" = install ] && [[ " $* " == *' -g '* ]]; then
 printf 'legacy-install\n' >> "$ROOT/counts"
 [ ! -f "$ROOT/legacy-fail" ] || exit 39
 package=${!#}; bin=probe
 if [[ "$package" == @*/*@* ]] || [[ "$package" != @* && "$package" == *@* ]]; then package=${package%@*}; fi
 prefix=$NPM_CONFIG_PREFIX;version=$(cat "$ROOT/version")
 mkdir -p "$prefix/bin" "$prefix/lib/node_modules/$package/bin"
 printf '{"version":"%s"}\n' "$version" > "$prefix/lib/node_modules/$package/package.json"
 printf '#!/usr/bin/env node\nconsole.log("%s");\n' "$version" > "$prefix/lib/node_modules/$package/bin/$bin"
 chmod 755 "$prefix/lib/node_modules/$package/bin/$bin"
 ln -sf "../lib/node_modules/$package/bin/$bin" "$prefix/bin/$bin"
 exit 0
fi
[ -z "${NPM_TOKEN:-}" ] && [ -z "${SYNTHETIC_PROVIDER_KEY:-}" ]
[ "$SSL_CERT_FILE" = /etc/ssl/certs/ca-bundle.crt ] && [ -z "$SSL_CERT_DIR" ] && [ "$NODE_OPTIONS" = --use-openssl-ca ]
prefix=''
previous=''
for arg in "$@"; do
 if [ "$previous" = --prefix ]; then prefix=$arg; fi
 previous=$arg
done
[ -n "$prefix" ]
if [ "$phase" = install ]; then
 printf 'resolve\n' >> "$ROOT/counts"
 printf started > "$ROOT/resolve-started"
 while [ -f "$ROOT/resolve-wait" ]; do sleep 0.02; done
 case " $* " in *' --package-lock-only '*) ;; *) exit 34;; esac
 case " $* " in *' --ignore-scripts '*) ;; *) exit 34;; esac
 [ ! -f "$ROOT/fail" ] || { printf '%2048s' '' | tr ' ' x >&2; printf '\nTLS verification failed; Authorization=fixture-secret-value\n' >&2; exit 35; }
 version=$(cat "$ROOT/version")
 package=$(jq -r '.dependencies | keys[0]' "$prefix/package.json")
 selector=$(jq -r --arg pkg "$package" '.dependencies[$pkg]' "$prefix/package.json")
 if [ "$selector" != latest ]; then version=$selector; fi
 resolved="https://synthetic.invalid/probe-$version.tgz"; [ ! -f "$ROOT/resolved" ] || resolved=$(cat "$ROOT/resolved")
 integrity="sha512-synthetic-$version"; [ ! -f "$ROOT/integrity" ] || integrity=$(cat "$ROOT/integrity")
 binmap='{"probe":"bin/probe","mcp-server-sequential-thinking":"bin/probe"}'; [ ! -f "$ROOT/binmap" ] || binmap=$(cat "$ROOT/binmap")
 cat > "$prefix/package-lock.json" <<EOF
{"lockfileVersion":3,"packages":{"":{"dependencies":{"$package":"$selector"}},"node_modules/$package":{"version":"$version","resolved":"$resolved","integrity":"$integrity","bin":$binmap}}}
EOF
 if [ -f "$ROOT/unsupported-lock" ]; then
  jq '.packages["node_modules/synthetic-git-dependency"]={"version":"1.0.0","resolved":"git+https://synthetic.invalid/dep.git#fixed"}' "$prefix/package-lock.json" > "$prefix/unsupported.json"
  mv "$prefix/unsupported.json" "$prefix/package-lock.json"
 fi
elif [ "$phase" = ci ]; then
 printf 'install\n' >> "$ROOT/counts"
 package=$(jq -r '.packages[""].dependencies | keys[0]' "$prefix/package-lock.json")
 version=$(jq -r --arg pkg "$package" '.packages["node_modules/"+$pkg].version' "$prefix/package-lock.json")
 mkdir -p "$prefix/node_modules/$package/bin" "$HOME/.config/login"
 printf secret > "$HOME/.config/login/token"
 printf '{"version":"%s"}\n' "$version" > "$prefix/node_modules/$package/package.json"
 binpath=bin/probe; [ ! -f "$ROOT/binpath" ] || binpath=$(cat "$ROOT/binpath")
 mkdir -p "$(dirname "$prefix/node_modules/$package/$binpath")"
 printf '#!/usr/bin/env node\nconsole.log("%s");\n' "$version" > "$prefix/node_modules/$package/$binpath"
 chmod 755 "$prefix/node_modules/$package/$binpath"
else exit 36; fi
`
	if err = os.WriteFile(filepath.Join(f.tools, "npm"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	oldChild, oldCmd, oldProbe := programCaptureChild, forkBuildChildCommand, probeForkBuildContainer
	t.Cleanup(func() {
		programCaptureChild = oldChild
		forkBuildChildCommand = oldCmd
		probeForkBuildContainer = oldProbe
	})
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) { return false, true }
	forkBuildChildCommand = func(args []string) (*exec.Cmd, error) {
		return exec.Command(self, append([]string{"npm-capture-runtime-test", f.root}, args...)...), nil
	}
	programCaptureChild = func(ctx context.Context, workspace string, argv []string, w io.Writer) error {
		if len(argv) > 2 && argv[2] == "program-npm" {
			f.resolves.Add(1)
		} else {
			f.installs.Add(1)
		}
		return oldChild(ctx, workspace, argv, w)
	}

	return f
}
func assertNPMCaptureUserConfigsRemoved(t *testing.T, root string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "npm-userconfigs"))
	if err != nil {
		t.Fatalf("producer npm commands did not report their private config: %v", err)
	}
	// One path per line: a temp root may contain spaces, so never split on whitespace.
	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	if len(paths) == 0 {
		t.Fatal("producer npm command observer saw no private userconfig")
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("owned producer npm config survived its command lifetime: %s (%v)", path, err)
		}
	}
}

func (f *selectedNPMFixture) version(t *testing.T, v string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, "version"), []byte(v), 0644); err != nil {
		t.Fatal(err)
	}
}
func (f *selectedNPMFixture) advance(t *testing.T, fresh bool) npmCaptureRecord {
	t.Helper()
	boundary, err := registerNPMCaptureRequest(context.Background(), f.store, f.in)
	if err != nil {
		t.Fatal(err)
	}
	r, err := advanceNPMCapture(context.Background(), f.store, f.in, "recipe-v1", fresh, boundary, io.Discard, func(commit func() error) error { return commit() })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNPMCaptureEnvironmentUsesOnlyTheImageTrustStore(t *testing.T) {
	for name, value := range map[string]string{
		"SSL_CERT_FILE":           "/caller/private-ca.pem",
		"SSL_CERT_DIR":            "/caller/private-ca-dir",
		"NIX_SSL_CERT_FILE":       "/caller/private-nix-ca.pem",
		"NODE_EXTRA_CA_CERTS":     "/caller/private-node-ca.pem",
		"HTTPS_PROXY":             "http://caller-proxy.invalid:3128",
		"NPM_CONFIG_USERCONFIG":   "/caller/.npmrc",
		"NPM_CONFIG_GLOBALCONFIG": "/caller/global.npmrc",
	} {
		t.Setenv(name, value)
	}
	userConfig, cleanup, err := createNPMCaptureUserConfig()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got := map[string]string{}
	for _, kv := range npmCaptureEnvironment(t.TempDir(), "/nix/store/node/bin/node", userConfig) {
		name, value, ok := strings.Cut(kv, "=")
		if ok {
			got[name] = value
		}
	}
	if got["SSL_CERT_FILE"] != "/etc/ssl/certs/ca-bundle.crt" || got["SSL_CERT_DIR"] != "" || got["NODE_OPTIONS"] != "--use-openssl-ca" {
		t.Fatalf("producer did not select the fixed image CA bundle with verified OpenSSL trust: %v", got)
	}
	if got["NPM_CONFIG_USERCONFIG"] != userConfig || got["NPM_CONFIG_USERCONFIG"] == got["NPM_CONFIG_GLOBALCONFIG"] || got["NPM_CONFIG_GLOBALCONFIG"] != "/dev/null" {
		t.Fatalf("producer did not select a private npm user config distinct from the global config: %v", got)
	}
	for _, name := range []string{"NIX_SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "HTTPS_PROXY"} {
		if _, ok := got[name]; ok {
			t.Errorf("caller trust/transport input %s crossed into the sealed producer", name)
		}
	}
}

func TestNPMCapturePrivateUserConfigIsEmptyAndRemoved(t *testing.T) {
	path, cleanup, err := createNPMCaptureUserConfig()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 || !filepath.IsAbs(path) {
		t.Fatalf("producer user config is not an absolute empty regular file: %s %+v %v", path, info, err)
	}
	cleanup()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("owned npm config was not removed after its command lifetime: %v", err)
	}
}

func TestNPMCaptureTrustStoreMustBeReadableRegularFile(t *testing.T) {
	if err := validateNPMCaptureTrustStore(filepath.Join(t.TempDir(), "missing.pem")); err == nil || !strings.Contains(err.Error(), "repair the selected image") {
		t.Fatalf("missing image trust store did not fail closed with recovery guidance: %v", err)
	}
	if err := validateNPMCaptureTrustStore(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("non-file trust store did not fail closed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ca-bundle.crt")
	if err := os.WriteFile(path, []byte("synthetic readable fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateNPMCaptureTrustStore(path); err != nil {
		t.Fatalf("readable regular fixture rejected: %v", err)
	}
}

func TestNPMCaptureNodeBoundaryValidatesImageTrustBeforeMise(t *testing.T) {
	for name, value := range map[string]string{
		"SSL_CERT_FILE":       "/caller/private-ca.pem",
		"SSL_CERT_DIR":        "/caller/private-ca-dir",
		"NIX_SSL_CERT_FILE":   "/caller/private-nix-ca.pem",
		"NODE_EXTRA_CA_CERTS": "/caller/private-node-ca.pem",
		"HTTPS_PROXY":         "http://caller-proxy.invalid:3128",
	} {
		t.Setenv(name, value)
	}

	for _, tc := range []struct {
		name      string
		prepare   func(string) func(string) error
		wantError string
	}{
		{
			name:      "missing",
			prepare:   func(string) func(string) error { return validateNPMCaptureTrustStore },
			wantError: "unavailable",
		},
		{
			name: "unreadable",
			prepare: func(string) func(string) error {
				return func(path string) error { return fmt.Errorf("open %s: %w", path, os.ErrPermission) }
			},
			wantError: "permission denied",
		},
		{
			name:      "non-regular",
			prepare:   func(string) func(string) error { return validateNPMCaptureTrustStore },
			wantError: "not a regular file",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trustPath := filepath.Join(t.TempDir(), "ca-bundle.crt")
			if tc.name == "non-regular" {
				if err := os.Mkdir(trustPath, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			userConfig, cleanup, err := createNPMCaptureUserConfig()
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			nodeRoot := t.TempDir()
			if err := os.Mkdir(filepath.Join(nodeRoot, "bin"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nodeRoot, "bin", "node"), []byte("fixture node"), 0o700); err != nil {
				t.Fatal(err)
			}
			var commands int
			ops := npmCaptureNodeIO{
				trustStorePath:   trustPath,
				validateTrust:    tc.prepare(trustPath),
				resolveNodeFloor: func(string) string { return "" },
				lookPath: func(string) (string, error) {
					t.Fatal("trust refusal must happen before PATH resolution")
					return "", nil
				},
				runCommand: func(_ context.Context, _ string, args, _ []string, stdout, _ io.Writer) error {
					commands++
					if len(args) > 0 && args[0] == "where" {
						_, _ = io.WriteString(stdout, nodeRoot+"\n")
					}
					return nil
				},
			}
			_, err = npmCaptureNodeWithIO(context.Background(), t.TempDir(), "24", userConfig, io.Discard, ops)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("trust failure %q did not reject at the node boundary: %v", tc.name, err)
			}
			if commands != 0 {
				t.Fatalf("trust failure started %d mise command(s)", commands)
			}
		})
	}
}

func TestNPMCaptureNodeBoundaryAllowsOnlyFixedImageTrust(t *testing.T) {
	trustPath := filepath.Join(t.TempDir(), "ca-bundle.crt")
	if err := os.WriteFile(trustPath, []byte("synthetic image CA bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	nodeRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(nodeRoot, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(nodeRoot, "bin", "node")
	if err := os.WriteFile(node, []byte("synthetic node"), 0o700); err != nil {
		t.Fatal(err)
	}
	userConfig, cleanup, err := createNPMCaptureUserConfig()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var commands []string
	var commandEnvs [][]string
	ops := npmCaptureNodeIO{
		trustStorePath:   trustPath,
		validateTrust:    validateNPMCaptureTrustStore,
		resolveNodeFloor: func(string) string { return "" },
		lookPath: func(string) (string, error) {
			t.Fatal("a declared floor should resolve via mise in this fixture")
			return "", nil
		},
		runCommand: func(_ context.Context, name string, args, env []string, stdout, _ io.Writer) error {
			commands = append(commands, name+" "+strings.Join(args, " "))
			commandEnvs = append(commandEnvs, append([]string(nil), env...))
			if name == "mise" && len(args) > 0 && args[0] == "where" {
				_, _ = io.WriteString(stdout, nodeRoot+"\n")
			}
			return nil
		},
	}
	got, err := npmCaptureNodeWithIO(context.Background(), t.TempDir(), "24", userConfig, io.Discard, ops)
	if err != nil || got != node {
		t.Fatalf("intact image trust did not permit fixed-floor Node resolution: %q %v", got, err)
	}
	if !slices.Equal(commands, []string{"mise install node@24", "mise where node@24"}) {
		t.Fatalf("resolved unexpected commands after image trust validation: %q", commands)
	}
	for _, env := range commandEnvs {
		gotEnv := map[string]string{}
		for _, kv := range env {
			if key, value, ok := strings.Cut(kv, "="); ok {
				gotEnv[key] = value
			}
		}
		if gotEnv["SSL_CERT_FILE"] != npmCaptureTrustStorePath || gotEnv["SSL_CERT_DIR"] != "" || gotEnv["NODE_OPTIONS"] != "--use-openssl-ca" {
			t.Errorf("mise did not receive fixed image trust settings: %v", gotEnv)
		}
		if gotEnv["NPM_CONFIG_USERCONFIG"] != userConfig || gotEnv["NPM_CONFIG_USERCONFIG"] == gotEnv["NPM_CONFIG_GLOBALCONFIG"] || gotEnv["NPM_CONFIG_GLOBALCONFIG"] != "/dev/null" {
			t.Errorf("npm config files did not use the private-user/global split: %v", gotEnv)
		}
		for _, name := range []string{"NIX_SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "HTTPS_PROXY"} {
			if _, ok := gotEnv[name]; ok {
				t.Errorf("caller trust/transport input %s crossed the node boundary", name)
			}
		}
	}
}

func TestSelectedNPMProducerActualColdWarmFreshAndFixedPin(t *testing.T) {
	f := newSelectedNPMFixture(t)
	cold := f.advance(t, false)
	assertNPMCaptureUserConfigsRemoved(t, f.root)
	if f.resolves.Load() != 1 || f.installs.Load() != 1 || cold.Identity == "" {
		t.Fatalf("cold counts %d/%d %+v", f.resolves.Load(), f.installs.Load(), cold)
	}
	for _, home := range []string{t.TempDir(), t.TempDir()} {
		if rc := runInternal([]string{"capture-materialize", "--store=" + f.store.Dir, "--home=" + home, "--bin=probe", "--compatibility=" + cold.Identity}); rc != 0 {
			t.Fatal(rc)
		}
		cmd := exec.Command(filepath.Join(home, ".npm-global/bin/probe"))
		got, err := cmd.Output()
		if err != nil || string(got) != "1.0.0\n" {
			t.Fatalf("%q %v", got, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".config/login/token")); !os.IsNotExist(err) {
			t.Fatal("installer login/config bytes reached consumer")
		}
	}
	warm := f.advance(t, false)
	if warm.Entry != cold.Entry || f.resolves.Load() != 1 || f.installs.Load() != 1 {
		t.Fatal("warm path contacted resolver or installer")
	}
	unchanged := f.advance(t, true)
	if unchanged.Entry != cold.Entry || f.resolves.Load() != 2 || f.installs.Load() != 1 {
		t.Fatal("fresh unchanged rebuilt instead of checking")
	}
	f.version(t, "2.0.0")
	newer := f.advance(t, true)
	if newer.Identity == cold.Identity || f.resolves.Load() != 3 || f.installs.Load() != 2 {
		t.Fatal("fresh update did not prepare newer exact release")
	}
	f.in.Package = "probe-pkg@1.0.0"
	fixed := f.advance(t, true)
	if fixed.Resolution.Release != "1.0.0" {
		t.Fatal("fixed version advanced to registry latest")
	}
}

func TestSelectedNPMFreshFailureCancelAndStaleDoNotPublish(t *testing.T) {
	f := newSelectedNPMFixture(t)
	good := f.advance(t, false)
	if err := os.WriteFile(filepath.Join(f.root, "fail"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	boundary, _ := registerNPMCaptureRequest(context.Background(), f.store, f.in)
	_, err := advanceNPMCapture(context.Background(), f.store, f.in, "recipe-v1", true, boundary, io.Discard, func(commit func() error) error { return commit() })
	if err == nil {
		t.Fatal("failed fresh check served old good as successful freshness")
	}
	r, _ := readNPMCaptureRecord(f.store, f.in)
	if r.Entry != good.Entry || r.Failure == "" {
		t.Fatal("failure replaced retained serving entry")
	}
	_ = os.Remove(filepath.Join(f.root, "fail"))
	f.version(t, "2.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = advanceNPMCapture(ctx, f.store, f.in, "recipe-v1", true, boundary, io.Discard, func(commit func() error) error { return commit() })
	if err == nil {
		t.Fatal("cancelled request completed")
	}
	var calls int
	stale := errors.New("selected declaration changed")
	_, err = advanceNPMCapture(context.Background(), f.store, f.in, "recipe-v1", true, boundary, io.Discard, func(commit func() error) error {
		calls++
		if calls > 1 {
			return stale
		}
		return commit()
	})
	if !errors.Is(err, stale) {
		t.Fatalf("stale publication: %v", err)
	}
	r, _ = readNPMCaptureRecord(f.store, f.in)
	if r.Entry != good.Entry {
		t.Fatal("stale generation published")
	}
}

func TestSelectedNPMProducerFailureDiagnosticIsBoundedAndRedacted(t *testing.T) {
	f := newSelectedNPMFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, "fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	boundary, err := registerNPMCaptureRequest(context.Background(), f.store, f.in)
	if err != nil {
		t.Fatal(err)
	}
	_, err = advanceNPMCapture(context.Background(), f.store, f.in, "recipe-v1", false, boundary, io.Discard, func(commit func() error) error { return commit() })
	if err == nil {
		t.Fatal("failed selected producer succeeded")
	}
	if !strings.Contains(err.Error(), "TLS verification failed") {
		t.Fatalf("producer cause did not survive the public preparation error: %v", err)
	}
	if strings.Contains(err.Error(), "fixture-secret-value") || !strings.Contains(err.Error(), "Authorization=<redacted>") {
		t.Fatalf("producer diagnostic was not redacted: %v", err)
	}
	const maxDiagnosticBytes = 1024
	if len(err.Error()) > maxDiagnosticBytes+256 {
		t.Fatalf("producer diagnostic exceeded its bound: %d bytes", len(err.Error()))
	}
}

func TestSelectedNPMResolutionObservesRuntimeAndRejectsLockConflict(t *testing.T) {
	f := newSelectedNPMFixture(t)
	r, err := resolveSelectedNPMCapture(context.Background(), f.store, f.in, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := r.inputs()
	if err != nil {
		t.Fatal(err)
	}
	if inputs.Release == "latest" || inputs.NodeABI == "" || len(inputs.Dependencies) != 1 || r.Runtime.NodeDigest == "" || r.Runtime.TrustStore != npmCaptureTrustStore {
		t.Fatal("fabricated resolution facts or missing image trust provenance")
	}
	original, _ := inputs.Identity()
	copy := *r
	copy.Runtime.ABI += "-other"
	other, _ := copy.inputs()
	identity, _ := other.Identity()
	if identity == original {
		t.Fatal("ABI conflict reused identity")
	}
	copy = *r
	copy.Runtime.Platform = "darwin/arm64"
	other, _ = copy.inputs()
	identity, _ = other.Identity()
	if identity == original {
		t.Fatal("platform conflict reused identity")
	}
	copy = *r
	copy.Runtime.TrustStore += "-other"
	if _, err := copy.inputs(); err == nil {
		t.Fatal("resolution accepted an unknown trust store")
	}
	copy = *r
	copy.Runtime.TrustStore = ""
	legacyInputs, err := copy.inputs()
	if err != nil {
		t.Fatalf("historical Good without a trust-store field became unusable: %v", err)
	}
	legacyIdentity, _ := legacyInputs.Identity()
	if legacyIdentity == original || strings.Contains(legacyInputs.Runtime, "TrustStore") {
		t.Fatal("new trust policy did not produce a distinct key while preserving the historical input shape")
	}
	copy = *r
	copy.Release = "99"
	if _, err := copy.inputs(); err == nil {
		t.Fatal("resolution disagreed with lock")
	}
	var b bytes.Buffer
	raw, _ := json.Marshal(r)
	file := filepath.Join(t.TempDir(), "resolution.json")
	_ = os.WriteFile(file, raw, 0644)
	old := npmCaptureImagePath
	npmCaptureImagePath = filepath.Join(t.TempDir(), "missing")
	defer func() { npmCaptureImagePath = old }()
	if rc := runProgramNPM([]string{"install", file, original}, &b); rc == 0 {
		t.Fatal("unobserved installer runtime accepted")
	}
}

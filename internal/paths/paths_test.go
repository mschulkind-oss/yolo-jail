package paths

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

// TestHomeResolution pins the audit-confirmed Python Path.home() parity: the
// paths constants must stay ABSOLUTE even when $HOME is unset or empty
// (Go's os.UserHomeDir would error there and yield relative paths).
func TestHomeResolution(t *testing.T) {
	orig, had := os.LookupEnv("HOME")
	t.Cleanup(func() {
		if had {
			os.Setenv("HOME", orig)
		} else {
			os.Unsetenv("HOME")
		}
	})

	// $HOME set and non-empty -> $HOME.
	os.Setenv("HOME", "/home/someone")
	if got := home(); got != "/home/someone" {
		t.Errorf("home() with HOME=/home/someone = %q, want /home/someone", got)
	}
	if got := GlobalStorage(); got != "/home/someone/.local/share/yolo-jail" {
		t.Errorf("GlobalStorage = %q", got)
	}

	// $HOME empty -> "/" (Python expanduser: userhome="" then `or "/"`).
	os.Setenv("HOME", "")
	if got := home(); got != "/" {
		t.Errorf("home() with HOME='' = %q, want /", got)
	}
	if got := GlobalStorage(); got != "/.local/share/yolo-jail" {
		t.Errorf("GlobalStorage with empty HOME = %q, want /.local/share/yolo-jail", got)
	}

	// $HOME unset -> passwd database home (Python pwd.getpwuid), which must be
	// absolute — never a relative path.
	os.Unsetenv("HOME")
	got := home()
	if got == "" || got[0] != '/' {
		t.Errorf("home() with HOME unset = %q, want an absolute passwd-db path", got)
	}
	// Sanity: it should match the current user's passwd home when available.
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		if got != u.HomeDir {
			t.Errorf("home() unset = %q, want passwd home %q", got, u.HomeDir)
		}
	}
}

// The conventional local pack sits BESIDE config.jsonc, and is DERIVED from it. That is the
// whole argument for the location — user-scope yolo config already lives there, so the
// convention extends an existing one rather than inventing a second place to remember — and a
// pair of independently-spelled suffixes could drift apart while both looked right.
func TestLocalPackDirIsBesideTheUserConfig(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	if got, want := LocalPackDir(), "/home/someone/.config/yolo-jail/local"; got != want {
		t.Errorf("LocalPackDir = %q, want %q", got, want)
	}
	if got, want := filepath.Dir(LocalPackDir()), filepath.Dir(UserConfigPath()); got != want {
		t.Errorf("LocalPackDir's parent %q is not the user config's dir %q — the convention is "+
			"\"beside config.jsonc\", so the two must share a parent by construction", got, want)
	}
	// Absolute even in a stripped environment, like every other path helper (see
	// TestHomeResolution): a relative pack root would be resolved against the process's cwd.
	t.Setenv("HOME", "")
	if got := LocalPackDir(); got[0] != '/' {
		t.Errorf("LocalPackDir with an empty HOME = %q, want an absolute path", got)
	}
}

// TestServiceEndpointConstants pins the producer/consumer contract for a host
// service's published endpoint file.
//
// These are pinned rather than trusted because the repo has already paid for a
// drifted name once: CgdSocketName's own comment records a refactor that kept a
// legacy spelling here and silently disabled the cgroup delegate in every jail.
// The run pipeline writes YOLO_SERVICE_<NAME>_ENDPOINT; yolo-ps, the OAuth
// terminator and the entrypoint's generated clients read it. Nothing catches a
// mismatch at build time.
func TestServiceEndpointConstants(t *testing.T) {
	if ServiceEndpointExt != ".endpoint" {
		t.Errorf("ServiceEndpointExt = %q, want \".endpoint\"", ServiceEndpointExt)
	}
	if ServiceEnvVarPrefix != "YOLO_SERVICE_" {
		t.Errorf("ServiceEnvVarPrefix = %q", ServiceEnvVarPrefix)
	}
	if ServiceEnvVarSuffix != "_ENDPOINT" {
		t.Errorf("ServiceEnvVarSuffix = %q", ServiceEnvVarSuffix)
	}
	// The composed name a consumer actually reads.
	if got, want := ServiceEnvVarPrefix+"HOST_PROCESSES"+ServiceEnvVarSuffix,
		"YOLO_SERVICE_HOST_PROCESSES_ENDPOINT"; got != want {
		t.Errorf("composed env var = %q, want %q", got, want)
	}
	// _SOCKET must not creep back as a second, drifting spelling.
	if ServiceEnvVarSuffix == "_SOCKET" {
		t.Error("the endpoint env var still says _SOCKET; the value is a file path, not a socket")
	}
}

// TestCgdEndpointNameIsComposed keeps the delegate's endpoint filename derived
// from its loophole name, for the same reason CgdSocketName is. Two independently
// spelled strings can both look right while naming different files.
func TestCgdEndpointNameIsComposed(t *testing.T) {
	if got, want := CgdEndpointName, "cgroup-delegate.endpoint"; got != want {
		t.Errorf("CgdEndpointName = %q, want %q", got, want)
	}
	if CgdEndpointName != BuiltinCgroupLoopholeName+ServiceEndpointExt {
		t.Errorf("CgdEndpointName %q is not composed from BuiltinCgroupLoopholeName %q + %q",
			CgdEndpointName, BuiltinCgroupLoopholeName, ServiceEndpointExt)
	}
	// The socket name and the endpoint name must name DIFFERENT files: during the
	// migration both spellings exist, and collapsing them would make a jail dial
	// an endpoint file as a socket.
	if CgdEndpointName == CgdSocketName {
		t.Error("CgdEndpointName and CgdSocketName are the same string")
	}
}

// TestJailHostServicesDirIsStable pins the in-jail mount point every consumer
// hardcodes in its own error text and every manifest's jail_endpoint must sit
// under.
func TestJailHostServicesDirIsStable(t *testing.T) {
	if got, want := JailHostServicesDir, "/run/yolo-services"; got != want {
		t.Errorf("JailHostServicesDir = %q, want %q", got, want)
	}
	if got, want := JailHostServicesDir+"/"+CgdEndpointName,
		"/run/yolo-services/cgroup-delegate.endpoint"; got != want {
		t.Errorf("composed delegate endpoint path = %q, want %q", got, want)
	}
}

// TestJailShortHashAndHostServicesDir pins the per-jail key and directory by
// VALUE, not by re-deriving them.
//
// Three packages carried hand-copied implementations of this hash before it moved
// here: the run pipeline (which creates the directory and spawns the relay),
// internal/cli/check (which probes them) and internal/prune (whose reap matches a
// broker-relay pid file back to a LIVE CONTAINER NAME through exactly this value).
// A drift there does not fail loudly — it either orphans every relay forever or
// kills a live one. Recomputing sha1 in the assertion would pin nothing, so the
// expected strings are literals taken from the shipped behaviour.
func TestJailShortHashAndHostServicesDir(t *testing.T) {
	// sha1("yolo-ws-abcd1234") = 0420db187f015c2f... (verified against sha1sum),
	// truncated to 8 hex chars.
	const cname = "yolo-ws-abcd1234"
	const wantHash = "0420db18"
	if got := JailShortHash(cname); got != wantHash {
		t.Errorf("JailShortHash(%q) = %q, want %q", cname, got, wantHash)
	}
	if got, want := HostServicesDirName(wantHash), "yolo-host-services-0420db18"; got != want {
		t.Errorf("HostServicesDirName = %q, want %q", got, want)
	}
	if got, want := HostServicesDir(cname, false), "/tmp/yolo-host-services-0420db18"; got != want {
		t.Errorf("HostServicesDir = %q, want %q", got, want)
	}
	// Distinct names must not collide into one directory: two jails sharing a
	// host-services dir would share each other's endpoint files, and each of those
	// carries a bearer token.
	if HostServicesDir("yolo-a", false) == HostServicesDir("yolo-b", false) {
		t.Error("two container names produced the same host-services dir")
	}
}

func TestUserConfigPathFallsBackToJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Neither exists -> returns config.jsonc default path
	wantJSONC := filepath.Join(tmp, ".config/yolo-jail/config.jsonc")
	if got := UserConfigPath(); got != wantJSONC {
		t.Errorf("UserConfigPath() = %q, want %q", got, wantJSONC)
	}

	// Create config.json only -> returns config.json
	cfgDir := filepath.Join(tmp, ".config/yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(cfgDir, "config.json")
	if err := os.WriteFile(jsonPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := UserConfigPath(); got != jsonPath {
		t.Errorf("UserConfigPath() = %q, want %q", got, jsonPath)
	}

	// Create config.jsonc -> returns config.jsonc (jsonc takes precedence)
	if err := os.WriteFile(wantJSONC, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := UserConfigPath(); got != wantJSONC {
		t.Errorf("UserConfigPath() = %q, want %q", got, wantJSONC)
	}
}

// TestWrapDirUnderExplicitHome pins the host wrapper dir's location AND the reason it
// takes an explicit home: `apply` renders into a home it was handed, so a wrap dir
// derived from $HOME would write into the invoking user's real state dir the moment the
// two differ. The Under form must therefore ignore $HOME entirely.
func TestWrapDirUnderExplicitHome(t *testing.T) {
	orig, had := os.LookupEnv("HOME")
	t.Cleanup(func() {
		if had {
			os.Setenv("HOME", orig)
		} else {
			os.Unsetenv("HOME")
		}
	})
	os.Setenv("HOME", "/home/invoking-user")

	const rendered = "/tmp/rendered-home"
	want := "/tmp/rendered-home/.local/share/yolo-jail/bin/wrap"
	if got := WrapDirUnder(rendered); got != want {
		t.Errorf("WrapDirUnder(%q) = %q, want %q", rendered, got, want)
	}
	if got := GeneratedBinDirUnder(rendered); got != "/tmp/rendered-home/.local/share/yolo-jail/bin" {
		t.Errorf("GeneratedBinDirUnder(%q) = %q", rendered, got)
	}

	// The $HOME-reading form stays consistent with GlobalStorage.
	if got, want := WrapDir(), filepath.Join(GlobalStorage(), "bin", "wrap"); got != want {
		t.Errorf("WrapDir() = %q, want %q", got, want)
	}
}

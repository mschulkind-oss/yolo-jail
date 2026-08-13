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

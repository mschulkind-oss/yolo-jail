package callbroker

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/s3fixture"
)

func TestS3FixtureBuildsOutsideCredentialHome(t *testing.T) {
	original := os.Environ()
	originalHome := buildEnvValue(original, "HOME")
	t.Setenv("YOLO_TEST_MATCHED_YOLO", "") // exercise both real build call sites
	builds := 0
	f := newS3E2EWithBuild(t, func(cmd *exec.Cmd) ([]byte, error) {
		builds++
		env := cmd.Environ()
		for _, key := range []string{"HOME", "GOCACHE", "GOMODCACHE", "GOENV", "GOCACHEPROG", "GOPATH", "XDG_CACHE_HOME"} {
			if got, want := buildEnvValue(env, key), buildEnvValue(original, key); got != want {
				t.Errorf("fixture build %s changed %s: got %q, want %q", cmd.Args[len(cmd.Args)-1], key, got, want)
			}
		}
		if home := buildEnvValue(env, "HOME"); home != originalHome {
			// Model Darwin's HOME-derived cache without changing the real shared Go cache.
			// Compiler artifacts embed the synthetic secret, just like the CI's *-d files.
			artifact := filepath.Join(home, "Library", "Caches", "go-build", "81", "fixture-d")
			if err := os.MkdirAll(filepath.Dir(artifact), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(artifact, []byte(s3fixture.SecretAccessKey), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return cmd.CombinedOutput()
	})
	if builds != 2 {
		t.Fatalf("got %d builds, want both yolo and fake host aws", builds)
	}
	artifact := filepath.Join(f.home, "Library", "Caches", "go-build", "81", "fixture-d")
	if data, err := os.ReadFile(artifact); err == nil && strings.Contains(string(data), s3fixture.SecretAccessKey) {
		t.Errorf("host record %s contains the secret", artifact)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if os.Getenv("HOME") != f.home {
		t.Fatal("executing the built CLI must still use the isolated credential home")
	}
}

func TestCallBrokerTestBuildPreservesOwnerGoOverrides(t *testing.T) {
	original := callBrokerBuildEnv
	t.Cleanup(func() { callBrokerBuildEnv = original })
	callBrokerBuildEnv = []string{
		"HOME=/owner/home",
		"GOCACHE=/owner/shared/build-cache",
		"GOMODCACHE=/owner/shared/module-cache",
		"GOENV=/owner/config/go-env",
		"GOCACHEPROG=/owner/bin/cache-program",
		"XDG_CACHE_HOME=/owner/shared/cache",
		"GOPATH=/owner/go",
	}
	t.Setenv("HOME", shortCallBrokerTempDir(t))
	cmd := callBrokerTestBuild("go", "fixture", "./testdata/classifier")
	if !reflect.DeepEqual(cmd.Env, callBrokerBuildEnv) {
		t.Fatalf("build environment = %q, want original owner environment %q", cmd.Env, callBrokerBuildEnv)
	}
}

func buildEnvValue(env []string, key string) string {
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			return value
		}
	}
	return ""
}

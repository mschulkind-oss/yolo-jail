package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func decodeCfg(t *testing.T, s string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("decode %q: not a map (%T)", s, v)
	}
	return m
}

// TestUserEnvChainRevokesOnConfigRemoval walks the whole host-side path that made
// commented-out credentials survive a rebuild: config.ResolveEnvSources feeding
// writeUserEnvFile, twice, exactly as run.go:311-312 does it. Launch 1 has
// env_sources; launch 2 has it commented out. The mounted file must end up empty
// — this is the assertion that maps 1:1 onto the reported symptom (AWS keys still
// exported in a freshly rebuilt jail).
func TestUserEnvChainRevokesOnConfigRemoval(t *testing.T) {
	ws := t.TempDir()
	credsFile := filepath.Join(ws, "creds.env")
	if err := os.WriteFile(credsFile,
		[]byte("AWS_ACCESS_KEY_ID=AKIASTALEKEY\nAWS_SECRET_ACCESS_KEY=stalesecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userEnvFile := filepath.Join(ws, "yolo-user-env.sh")

	// --- Launch 1: env_sources present. ---
	withSources := decodeCfg(t, `{"env_sources": ["creds.env"]}`)
	env1 := config.ResolveEnvSources(ws, withSources, nil)
	writeUserEnvFile(userEnvFile, env1)
	got1, err := os.ReadFile(userEnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(got1) == 0 {
		t.Fatalf("launch 1 should have rendered creds; got empty file")
	}

	// --- Launch 2: env_sources commented out, i.e. absent from the parsed config. ---
	withoutSources := decodeCfg(t, `{}`)
	env2 := config.ResolveEnvSources(ws, withoutSources, nil)
	writeUserEnvFile(userEnvFile, env2)

	got2, err := os.ReadFile(userEnvFile)
	if err != nil {
		t.Fatalf("file must survive as the bind-mount source: %v", err)
	}
	if len(got2) != 0 {
		t.Errorf("removing env_sources must revoke the creds; file still holds:\n%s", got2)
	}
}

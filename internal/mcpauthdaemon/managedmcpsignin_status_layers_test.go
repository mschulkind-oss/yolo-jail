package mcpauthdaemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func statusFixtureTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += string(body)
		}
		out[path] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStatusHandlerStrictLayersAndNoEffects(t *testing.T) {
	for _, mode := range []string{"missing", "healthy", "unsafe", "malformed source", "held source lock", "header only", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			f := newOnceDaemonFixture(t)
			if mode != "missing" {
				store := adoptHeaderFixture(t, f)
				switch mode {
				case "unsafe":
					if err := os.Chmod(filepath.Join(store.Dir, store.Filename), 0644); err != nil {
						t.Fatal(err)
					}
				case "malformed source":
					if err := os.WriteFile(filepath.Join(f.storeDir, ".credentials.json"), []byte("broken"), 0600); err != nil {
						t.Fatal(err)
					}
				case "held source lock":
					if err := os.Mkdir(filepath.Join(f.storeDir, ".storage-write.lock"), 0700); err != nil {
						t.Fatal(err)
					}
				case "header only":
					f.b.share.Sources = nil
				case "orphan":
					f.b.share.Once = nil
				}
			}
			before := statusFixtureTree(t, f.b.canonical.Dir)
			sourceBefore := statusFixtureTree(t, f.storeDir)
			var calls atomic.Int64
			previous := http.DefaultTransport
			http.DefaultTransport = countingMCPTransport{Next: previous, Calls: &calls}
			defer func() { http.DefaultTransport = previous }()
			reply := askBroker(t, f.b, map[string]any{"op": "status"})
			if reply.rc != 0 || !json.Valid([]byte(reply.stdout)) {
				t.Fatalf("readable unhealthy status: %+v", reply)
			}
			if calls.Load() != 0 || !reflect.DeepEqual(before, statusFixtureTree(t, f.b.canonical.Dir)) || !reflect.DeepEqual(sourceBefore, statusFixtureTree(t, f.storeDir)) {
				t.Fatal("status caused provider/repair/source-lock effects")
			}
			for _, secret := range []string{"at-1", "at-2", "rt-1", "rt-2"} {
				hash := sha256.Sum256([]byte(secret))
				if strings.Contains(reply.stdout+reply.stderr, secret) || strings.Contains(reply.stdout+reply.stderr, hex.EncodeToString(hash[:])) {
					t.Fatal("status leaked credential bytes/hash")
				}
			}
			if mode == "header only" && (!strings.Contains(reply.stdout, `"record"`) || !strings.Contains(reply.stdout, `"delivery"`) || !strings.Contains(reply.stdout, `"refresher"`)) {
				t.Fatal("header-only layers absent")
			}
			if mode == "orphan" {
				if strings.Contains(reply.stdout, "retained, unused") || strings.Contains(reply.stdout, `"slack"`) {
					t.Fatalf("a jail's status lists a record its share does not name: %s", reply.stdout)
				}
				host := ReadHostStatus(f.b.canonical.Dir, f.b.share)
				data, _ := json.Marshal(host)
				if !strings.Contains(string(data), "retained, unused") {
					t.Fatalf("host status lost the orphan row: %s", data)
				}
			}
		})
	}
}

func TestStatusOutcomeUnreadableAndUnknownVersionAreNotRepaired(t *testing.T) {
	f := newOnceDaemonFixture(t)
	store := adoptHeaderFixture(t, f)
	entries, err := os.ReadDir(store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "outcome.") {
			continue
		}
		path := filepath.Join(store.Dir, entry.Name())
		if err := os.WriteFile(path, []byte(`{"version":99}`), 0600); err != nil {
			t.Fatal(err)
		}
		before := statusFixtureTree(t, store.Dir)
		reply := askBroker(t, f.b, map[string]any{"op": "status"})
		if reply.rc != 0 || !strings.Contains(reply.stdout, `"outcome": "unavailable"`) || !reflect.DeepEqual(before, statusFixtureTree(t, store.Dir)) {
			t.Fatalf("unknown note repaired or status refused: %+v", reply)
		}
		return
	}
	t.Fatal("fixture outcome missing")
}

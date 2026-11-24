package brokerscope

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestScopeReaderRefusesUnknownSnapshotAuthority(t *testing.T) {
	t.Setenv("HOME", resolvedTempDir(t))
	f := File{Source: "tasks", LaunchID: "1234567890abcdef", Workspace: resolvedTempDir(t), PID: os.Getpid(), Container: "fixture", Form: FormCallRoot, Repos: []string{"team:alpha"}}
	path, err := Write(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record["policy_snapshot"] = json.RawMessage(`{"version":"future/999","name":"foreign.json","digest":"unknown","sequence":1}`)
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(path); err == nil {
		t.Fatal("scope reader ignored unrecognized snapshot authority and returned legacy roots")
	}
}

func snapshotFixture(t *testing.T) (File, PolicySnapshot, string) {
	t.Helper()
	t.Setenv("HOME", resolvedTempDir(t))
	f := File{Source: "tasks", LaunchID: "1234567890abcdef", Workspace: resolvedTempDir(t), PID: os.Getpid(), Container: "fixture", Form: FormCallRoot, Repos: []string{"team:alpha"}}
	path, err := Write(f)
	if err != nil {
		t.Fatal(err)
	}
	p := PolicySnapshot{
		Version:  PolicySnapshotVersion,
		Binding:  PolicyBinding{Source: f.Source, LaunchID: f.LaunchID, Workspace: f.Workspace, DeclarationHash: strings.Repeat("a", 64), InputDigest: strings.Repeat("b", 64)},
		Sequence: 1, Model: "scoped/1", Roots: []string{"team:alpha"},
		Asks: WorkspaceAsks{
			SourceRead: "false",
			Requests:   json.RawMessage(`{"read":{"list":"ask"},"all":{"change":false},"working_set":{"change":"ask"}}`),
			Scopes:     []WorkspaceAskScope{{Alias: "alpha", Resource: "team:alpha", Requests: json.RawMessage(`{"read":{"list":true},"all":{"change":"ask"}}`)}},
		},
	}
	f.Version = FileVersion
	return f, p, path
}

func TestSnapshotPreparationRefusesIncompleteOrAmbiguousAsks(t *testing.T) {
	for _, change := range []string{"source-read", "requests", "scopes", "duplicate", "version", "model", "roots"} {
		t.Run(change, func(t *testing.T) {
			f, p, path := snapshotFixture(t)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "source-read":
				p.Asks.SourceRead = ""
			case "requests":
				p.Asks.Requests = json.RawMessage(`null`)
			case "scopes":
				p.Asks.Scopes = nil
			case "duplicate":
				p.Asks.Requests = json.RawMessage(`{"read":true,"read":false}`)
			case "version":
				p.Version = "future/999"
			case "model":
				p.Model = "future/999"
			case "roots":
				p.Roots = nil
			}
			if err := brokerlock.WithTransaction(paths.BrokerSourceDir(f.Source), func() error {
				_, err := PreparePolicyLocked(f, p)
				return err
			}); err == nil {
				t.Fatal("prepared incomplete or ambiguous authority")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid preparation changed authority")
			}
		})
	}
}

func prepareSnapshot(t *testing.T, f File, p PolicySnapshot) *PolicyReference {
	t.Helper()
	var ref *PolicyReference
	err := brokerlock.WithTransaction(paths.BrokerSourceDir(f.Source), func() error {
		var err error
		ref, err = PreparePolicyLocked(f, p)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func snapshotBytes(t *testing.T, f File) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(paths.BrokerScopeFile(f.Source, f.LaunchID)), f.PolicySnapshot.Name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSnapshotPreparationIsNotAuthorityAndScopeCommitsCompletePayload(t *testing.T) {
	f, p, path := snapshotFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f.PolicySnapshot = prepareSnapshot(t, f, p)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("preparation changed authoritative scope")
	}
	if _, err := Write(f); err != nil {
		t.Fatal(err)
	}
	current, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadPolicy(current, p.Binding.DeclarationHash, p.Binding.InputDigest)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("complete roots/source read/read cells/write tables/scopes/model lost: %+v %v", got, err)
	}
	if _, err := ReadPolicy(current, strings.Repeat("c", 64), p.Binding.InputDigest); err == nil {
		t.Fatal("scope's self-declared declaration hash replaced immutable expected binding")
	}
	if _, err := ReadPolicy(current, p.Binding.DeclarationHash, strings.Repeat("c", 64)); err == nil {
		t.Fatal("copied input identity admitted")
	}
	if _, err := PreparePolicyLocked(current, p); err == nil {
		t.Fatal("non-advancing sequence admitted")
	}
	// Returned data is not a cached live policy.
	got.Roots[0] = "team:foreign"
	got.Asks.Requests[0] = '['
	got.Asks.Scopes[0].Requests[0] = '['
	again, err := ReadPolicy(current, p.Binding.DeclarationHash, p.Binding.InputDigest)
	if err != nil || !reflect.DeepEqual(again, p) {
		t.Fatal("reader reused mutable returned state")
	}
}

func TestSnapshotScopeWriterRejectsMirroredRootAndBindingMismatchWithoutCommit(t *testing.T) {
	for _, change := range []string{"roots", "source", "workspace", "launch", "model", "sequence", "locator", "declaration", "input"} {
		t.Run(change, func(t *testing.T) {
			f, p, path := snapshotFixture(t)
			f.PolicySnapshot = prepareSnapshot(t, f, p)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "roots":
				f.Repos = []string{"team:beta"}
			case "source":
				f.Source = "foreign"
			case "workspace":
				f.Workspace = resolvedTempDir(t)
			case "launch":
				f.LaunchID = "aaaaaaaaaaaaaaaa"
			case "model":
				f.PolicySnapshot.Model = "legacy/1"
			case "sequence":
				f.PolicySnapshot.Sequence++
			case "locator":
				f.PolicySnapshot.Name = "../foreign.json"
			case "declaration":
				f.PolicySnapshot.DeclarationHash = strings.Repeat("c", 64)
			case "input":
				f.PolicySnapshot.InputDigest = strings.Repeat("c", 64)
			}
			if _, err := Write(f); err == nil {
				t.Fatalf("scope writer committed %s mismatch", change)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed authority write changed old scope")
			}
		})
	}
}

func TestSnapshotScopeReaderRefusesTamperedPrivateAuthorityWithoutFallback(t *testing.T) {
	for _, change := range []string{"digest", "permission", "symlink", "directory", "missing", "mirrored-roots", "protected-directory", "unknown-field", "null-locator"} {
		t.Run(change, func(t *testing.T) {
			f, p, path := snapshotFixture(t)
			f.PolicySnapshot = prepareSnapshot(t, f, p)
			if _, err := Write(f); err != nil {
				t.Fatal(err)
			}
			policyPath := filepath.Join(filepath.Dir(path), f.PolicySnapshot.Name)
			switch change {
			case "digest":
				if err := os.WriteFile(policyPath, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "permission":
				if err := os.Chmod(policyPath, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink", "directory", "missing":
				data := snapshotBytes(t, f)
				if err := os.Remove(policyPath); err != nil {
					t.Fatal(err)
				}
				if change == "symlink" {
					target := filepath.Join(resolvedTempDir(t), "policy")
					if err := os.WriteFile(target, data, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, policyPath); err != nil {
						t.Fatal(err)
					}
				} else if change == "directory" {
					if err := os.Mkdir(policyPath, 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "null-locator":
				var raw map[string]json.RawMessage
				data, err := os.ReadFile(path)
				if err != nil || json.Unmarshal(data, &raw) != nil {
					t.Fatal("read scope fixture")
				}
				raw["policy_snapshot"] = json.RawMessage(`null`)
				data, err = json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "mirrored-roots":
				f.Repos = []string{"team:beta"}
				data, err := json.Marshal(f)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "protected-directory":
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
			case "unknown-field":
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(snapshotBytes(t, f), &raw); err != nil {
					t.Fatal(err)
				}
				raw["future"] = json.RawMessage(`true`)
				data, err := json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(policyPath); err != nil {
					t.Fatal(err)
				}
				f.PolicySnapshot.Digest = policyDigest(data)
				f.PolicySnapshot.Name = policyName(f.LaunchID, f.PolicySnapshot.Sequence, f.PolicySnapshot.Digest)
				policyPath = filepath.Join(filepath.Dir(path), f.PolicySnapshot.Name)
				if err := os.WriteFile(policyPath, data, 0600); err != nil {
					t.Fatal(err)
				}
				data, err = json.Marshal(f)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadFile(path); err == nil {
				t.Fatalf("scope reader accepted %s and fell back to mirrored roots", change)
			}
		})
	}
}

func TestLegacyScopeReplacementCannotMixSnapshotRootsAndAsks(t *testing.T) {
	f, p, path := snapshotFixture(t)
	f.PolicySnapshot = prepareSnapshot(t, f, p)
	if _, err := Write(f); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceScopeContext(context.Background(), path, f, []string{"team:beta"}, nil); err == nil {
		t.Fatal("legacy scope writer admitted roots without complete asks replacement")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy replacement changed snapshot authority")
	}
	current, err := ReadPolicy(f, p.Binding.DeclarationHash, p.Binding.InputDigest)
	if err != nil || !reflect.DeepEqual(current, p) {
		t.Fatal("legacy replacement changed sidecar authority")
	}
}

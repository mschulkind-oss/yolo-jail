package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateOfficialNodeContainsPhysicalCandidateAndSiblingBeforeExecution(t *testing.T) {
	f, _ := privateOfficialProducer(t)
	outside := t.TempDir()
	attempt := filepath.Join(outside, "nonprivate-executed")
	for _, name := range []string{"node", "npm", "mise"} {
		writeFile(t, filepath.Join(outside, name), "#!/bin/sh\nprintf attempted >> '"+attempt+"'\nexit 91\n")
		if err := os.Chmod(filepath.Join(outside, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", outside+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), testHangGuard(t))
	defer cancel()
	for _, floor := range []string{"", "22.12", "99", "invalid"} {
		node, err := privateOfficialNode(ctx, t.TempDir(), floor, "/dev/null", io.Discard)
		if floor == "99" || floor == "invalid" {
			if err == nil {
				t.Fatalf("unresolved floor %s selected %s", floor, node)
			}
		} else if err != nil || node != filepath.Join(f.root, "tools", "node") {
			t.Fatalf("physical private candidate floor=%s: %s %v", floor, node, err)
		}
		if _, err := os.Lstat(attempt); !os.IsNotExist(err) {
			t.Fatalf("nonprivate node/npm/mise executed: %v", err)
		}
	}
	nodePath := filepath.Join(f.root, "tools", "node")
	nodeBytes, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	npmPath := filepath.Join(f.root, "tools", "npm")
	npmBytes, err := os.ReadFile(npmPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"node-symlink", "node-bytes", "npm-symlink", "npm-bytes"} {
		t.Run(mutation, func(t *testing.T) {
			defer func() {
				_ = os.Remove(nodePath)
				_ = os.Remove(npmPath)
				if err := os.WriteFile(nodePath, nodeBytes, 0755); err != nil {
					t.Error(err)
				}
				if err := os.WriteFile(npmPath, npmBytes, 0755); err != nil {
					t.Error(err)
				}
			}()
			path, target := nodePath, filepath.Join(outside, "node")
			if strings.HasPrefix(mutation, "npm") {
				path, target = npmPath, filepath.Join(outside, "npm")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(mutation, "symlink") {
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("changed candidate"), 0755); err != nil {
				t.Fatal(err)
			}
			if _, err := privateOfficialNode(ctx, t.TempDir(), "22.12", "/dev/null", io.Discard); err == nil {
				t.Fatal("changed private node/npm pair was admitted")
			}
			if _, err := os.Lstat(attempt); !os.IsNotExist(err) {
				t.Fatalf("changed pair escaped to nonprivate executable: %v", err)
			}
		})
	}
}

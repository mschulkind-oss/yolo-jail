package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateOfficialActualSelfExecRefusesLostRoutingBeforeProducerEffects(t *testing.T) {
	privateOfficialProducer(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(t.TempDir(), "yolo-private-official")
	if err := os.WriteFile(driver, raw, 0755); err != nil {
		t.Fatal(err)
	}
	root, home := t.TempDir(), t.TempDir()
	setup := exec.Command(driver, "private-official-setup", root)
	setup.Env = os.Environ()
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("owned setup: %v %s", err, out)
	}
	writeFile(t, filepath.Join(root, "home-binding"), home)
	marker := filepath.Join(home, ".private-official-producer")
	writeFile(t, marker, root)
	for _, mutation := range []string{"marker-missing", "marker-corrupt", "home-mismatch", "npm-missing", "node-digest-missing"} {
		t.Run(mutation, func(t *testing.T) {
			path := marker
			if mutation == "home-mismatch" {
				path = filepath.Join(root, "home-binding")
			} else if mutation == "npm-missing" {
				path = filepath.Join(root, "tools", "npm")
			} else if mutation == "node-digest-missing" {
				path = filepath.Join(root, "node-digest")
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(path, original, info.Mode().Perm()); err != nil {
					t.Error(err)
				}
			}()
			if strings.HasSuffix(mutation, "missing") {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("corrupt"), info.Mode().Perm()); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"internal", "preparation-scheduler"},
				{"internal", "ordinary-preparation-worker"},
				{"npm-capture-runtime-test", root, "internal", programCaptureJailVerb, "missing-seal"},
			} {
				cmd := exec.Command(driver, args...)
				cmd.Env = append(os.Environ(), "HOME="+home)
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "private") {
					t.Fatalf("actual child did not refuse lost %s: %v\n%s", mutation, err, out)
				}
				for _, event := range []string{"private-execution-events", "resolved-executables", "private-official-events"} {
					if _, err := os.Lstat(filepath.Join(root, event)); !os.IsNotExist(err) {
						t.Fatalf("lost child routing reached executable/installer effects: %s %v", event, err)
					}
				}
			}
		})
	}
}

package run

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

func init() {
	if os.Getenv("YOLO_TEST_SOURCED_CLEANUP_CHILD") == "1" {
		if os.Getuid() == 0 {
			fmt.Fprintln(os.Stderr, "cleanup witness stayed root")
			os.Exit(2)
		}
		if os.Getenv("YOLO_TEST_ORDINARY_CLEANUP_CONTROL") == "1" {
			failed := false
			for i, kind := range []string{"unreadable-file", "foreign-empty-directory", "sealed-unreadable-file"} {
				for _, generic := range []bool{false, true} {
					cname := fmt.Sprintf("yolo-ordinary-%d-%v", i, generic)
					base := filepath.Join(os.Getenv("HOME"), "ordinary-controls")
					tree := filepath.Join(base, cname)
					if generic {
						base = paths.DiscardRoot(cname)
						tree = filepath.Join(base, "gone-1")
					}
					err := os.MkdirAll(tree, 0755)
					if err == nil {
						err = os.WriteFile(filepath.Join(tree, "aa-readable"), []byte("ordinary"), 0644)
					}
					if err == nil && (i == 0 || i == 2) {
						err = os.WriteFile(filepath.Join(tree, "zz-lock"), []byte("unreadable"), 0000)
					}
					if err == nil && i == 1 {
						err = os.Rename(filepath.Join(os.Getenv("HOME"), fmt.Sprintf("foreign-seed-%v", generic)), filepath.Join(tree, "zz-other-uid"))
					}
					if err != nil {
						fmt.Fprintln(os.Stderr, err)
						os.Exit(2)
					}
					if i == 2 {
						if err := os.Chmod(tree, 0555); err != nil {
							fmt.Fprintln(os.Stderr, err)
							os.Exit(2)
						}
					}
					if generic {
						rc := DiscardRemoverMain([]string{"--", cname})
						if rc != 0 {
							err = fmt.Errorf("discard-remover=%d", rc)
						}
					} else {
						err = packsrc.RemoveSealedSkillTree(base, tree)
					}
					_, remaining := os.Lstat(tree)
					fmt.Fprintf(os.Stdout, "uid=%d kind=%s generic=%v error=%v remaining=%v\n", os.Getuid(), kind, generic, err, remaining == nil)
					if err != nil || !os.IsNotExist(remaining) {
						failed = true
					}
				}
			}
			if failed {
				os.Exit(1)
			}
		}
		cname := "yolo-sourced-cleanup"
		base := paths.DiscardRoot(cname)
		marker := filepath.Join(base, "gone-1/tree/_skill-payloads/id/skills/x/SKILL.md")
		err := os.MkdirAll(filepath.Dir(marker), 0755)
		if err == nil {
			err = os.WriteFile(marker, []byte("marker"), 0444)
		}
		for dir := filepath.Dir(marker); err == nil && dir != filepath.Join(base, "gone-1/tree/_skill-payloads"); dir = filepath.Dir(dir) {
			err = os.Chmod(dir, 0555)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		rc := DiscardRemoverMain([]string{"--", cname})
		_, statErr := os.Lstat(base)
		fmt.Fprintf(os.Stdout, "uid=%d discard-remover=%d remaining=%v\n", os.Getuid(), rc, statErr == nil)
		_ = packsrc.RemoveSealedSkillTree(filepath.Dir(base), base)
		if rc != 0 || !os.IsNotExist(statErr) {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func TestDiscardRemoverDeletesSealedPayloadAsUnprivilegedOwner(t *testing.T) {
	if os.Getuid() != 0 {
		if os.Getuid() == 0 {
			t.Fatal("cleanup witness must be unprivileged")
		}
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
		cname := "yolo-sourced-cleanup"
		base := paths.DiscardRoot(cname)
		marker := filepath.Join(base, "gone-1/tree/_skill-payloads/id/skills/x/SKILL.md")
		if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, []byte("marker"), 0444); err != nil {
			t.Fatal(err)
		}
		for dir := filepath.Dir(marker); dir != filepath.Join(base, "gone-1/tree/_skill-payloads"); dir = filepath.Dir(dir) {
			if err := os.Chmod(dir, 0555); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() { _ = packsrc.RemoveSealedSkillTree(home, base) })
		if rc := DiscardRemoverMain([]string{"--", cname}); rc != 0 {
			t.Fatalf("owned production discard remover failed as uid %d", os.Getuid())
		}
		if _, err := os.Lstat(base); !os.IsNotExist(err) {
			t.Fatalf("sealed launch payload leaked: %v", err)
		}
		return
	}
	if runtime.GOOS != "linux" {
		t.Skip("root-to-unprivileged helper uses Linux proc descriptors; ordinary nonroot invocation runs directly")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0777); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("YOLO_TEST_ORDINARY_CLEANUP_CONTROL") == "1" {
		for _, generic := range []bool{false, true} {
			seed := filepath.Join(root, fmt.Sprintf("foreign-seed-%v", generic))
			if err := os.Mkdir(seed, 0777); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(seed, 0777); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Descriptor paths avoid granting traversal through the test harness's private
	// ancestors. Only this owned helper fixture is writable by the dropped uid.
	original, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	copyPath := filepath.Join(root, "cleanup.test")
	copy, err := os.OpenFile(copyPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(copy, original); err != nil {
		t.Fatal(err)
	}
	if err := copy.Close(); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Open(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer binary.Close()
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	cmd := exec.Command("/proc/self/fd/3", "-test.run=^TestDiscardRemoverDeletesSealedPayloadAsUnprivilegedOwner$", "-test.count=1")
	cmd.ExtraFiles = []*os.File{binary, dir}
	cmd.Dir = fmt.Sprintf("/proc/self/fd/%d", dir.Fd())
	cmd.Env = append(testtmp.ChildEnv(os.Environ()), "YOLO_TEST_SOURCED_CLEANUP_CHILD=1", "TMPDIR=.", "HOME=/proc/self/fd/4", "XDG_CONFIG_HOME=/proc/self/fd/4/config", "XDG_DATA_HOME=/proc/self/fd/4/data")
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unprivileged production helper: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "uid=65534 discard-remover=0 remaining=false") {
		t.Fatalf("missing cleanup witness: %s", output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

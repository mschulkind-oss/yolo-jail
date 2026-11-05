package paths

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The scoped callback swaps the leaf after the production Lstat and before the
// production no-follow open; no scheduler timing is needed.
func TestOpenRegularBeneathRefusesLeafSwapBeforeReadOrTruncate(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag int
	}{
		{"read", os.O_RDONLY}, {"truncate", os.O_WRONLY | os.O_CREATE | os.O_TRUNC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target")
			leaf := filepath.Join(dir, "leaf")
			const sentinel = "target must not be read or truncated"
			if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(leaf, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			swapped := false
			f, err := openRegularBeneathAfterCheck(root, "leaf", tc.flag, 0o600, func() {
				swapped = true
				if err := os.Remove(leaf); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("target", leaf); err != nil {
					t.Fatal(err)
				}
			})
			if f != nil {
				if tc.flag == os.O_RDONLY {
					data, readErr := io.ReadAll(f)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if string(data) == sentinel {
						t.Error("read the swapped link target")
					}
				}
				f.Close()
			}
			if !swapped {
				t.Fatal("did not reach the opening seam")
			}
			if err == nil {
				t.Fatal("accepted swapped linked leaf")
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != sentinel {
				t.Fatalf("target changed: %q, %v", data, err)
			}
		})
	}
}

func TestOpenRegularBeneathRejectsSwappedFIFOWithoutHanging(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "leaf")
	if err := os.WriteFile(leaf, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := openRegularBeneathAfterCheck(root, "leaf", os.O_RDONLY, 0, func() {
		if err := os.Remove(leaf); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(leaf, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if f != nil {
		f.Close()
	}
	if err == nil || !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("swapped FIFO error = %v", err)
	}
}

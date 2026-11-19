package integration

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

func privateDriverIdentity(path string, expected [32]byte) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("private suite admitted driver missing or linked")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return fmt.Errorf("private suite driver has linked ancestry")
	}
	raw, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(raw) != expected {
		return fmt.Errorf("private suite admitted executable identity changed; ordinary execution refused")
	}
	return nil
}

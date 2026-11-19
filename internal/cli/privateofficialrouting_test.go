package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func validatePrivateOfficialRoot(root string) error {
	info, err := os.Lstat(filepath.Join(os.Getenv("HOME"), ".private-official-producer"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return fmt.Errorf("private producer marker is missing, linked or not private")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("private producer marker owner mismatch")
	}
	marker, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".private-official-producer"))
	if err != nil || string(marker) != root {
		return fmt.Errorf("private producer marker binding changed")
	}
	info, err = os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return fmt.Errorf("private producer registry is missing, linked or not private")
	}
	binding := filepath.Join(root, "home-binding")
	boundInfo, err := os.Lstat(binding)
	if err != nil || !boundInfo.Mode().IsRegular() || boundInfo.Mode().Perm() != 0600 {
		return fmt.Errorf("private producer HOME binding missing or linked")
	}
	bound, err := os.ReadFile(binding)
	if err != nil || string(bound) != os.Getenv("HOME") {
		return fmt.Errorf("private producer HOME binding changed")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return fmt.Errorf("private producer registry has a linked ancestor")
	}
	for _, name := range []string{"node", "npm", "yolo"} {
		path := filepath.Join(root, "tools", name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
			return fmt.Errorf("private producer execution adapter missing or changed: %s", name)
		}
		if name != "node" {
			self, err := os.Executable()
			if err != nil {
				return err
			}
			command := "private-official-npm " + shquote.Quote(root)
			if name == "yolo" {
				command = "npm-capture-test-image " + shquote.Quote(filepath.Join(root, "image"))
			}
			expected := "#!/bin/sh\nexec " + shquote.Quote(self) + " " + command + " \"$@\"\n"
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != expected {
				return fmt.Errorf("private producer adapter binding changed: %s", name)
			}
		}
	}
	return privateOfficialNodePair(root)
}

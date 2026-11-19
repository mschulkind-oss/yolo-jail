package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// Only the physical candidate copied by this fixture may execute. The normal
// version/floor comparator and producer trust/lock/admission code remain intact.
func privateOfficialNode(ctx context.Context, home, floor, userConfig string, w io.Writer) (string, error) {
	root := filepath.Dir(npmCaptureImagePath)
	node := filepath.Join(root, "tools", "node")
	if err := privateOfficialNodePair(root); err != nil {
		return "", err
	}
	var version string
	resolve := func(requested string) string {
		if !packdecl.ValidNodeFloor(requested) {
			return ""
		}
		cmd := exec.CommandContext(ctx, node, "--version")
		out, err := cmd.Output()
		version = strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
		if err != nil || !packdecl.SatisfiesNodeFloor(version, requested) {
			return ""
		}
		return node
	}
	selected, err := npmCaptureNodeWithIO(ctx, home, floor, userConfig, w, npmCaptureNodeIO{
		trustStorePath: filepath.Join(root, "synthetic-ca.pem"), validateTrust: validateNPMCaptureTrustStore,
		resolveNodeFloor: resolve,
		lookPath: func(name string) (string, error) {
			if name != "node" {
				return "", fmt.Errorf("private node resolver rejects other binaries")
			}
			return node, nil
		},
		runCommand: func(context.Context, string, []string, []string, io.Writer, io.Writer) error {
			return fmt.Errorf("private node floor %s unresolved at observed %s; external mise/npm execution refused", floor, version)
		},
	})
	if err != nil {
		return "", err
	}
	if selected != node || privateOfficialNodePair(root) != nil {
		return "", fmt.Errorf("private node/sibling npm containment changed")
	}
	f, err := os.OpenFile(filepath.Join(root, "resolved-executables"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := fmt.Fprintf(f, "pid=%d home=%s floor=%s node=%s npm=%s\n", os.Getpid(), home, floor, node, filepath.Join(filepath.Dir(node), "npm"))
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return selected, nil
}

func privateOfficialNodePair(root string) error {
	for _, name := range []string{"node", "npm"} {
		path := filepath.Join(root, "tools", name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
			return fmt.Errorf("private node pair missing or linked: %s", name)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || resolved != path {
			return fmt.Errorf("private node pair escapes its owned anchor: %s", name)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "tools", "node"))
	if err != nil {
		return err
	}
	digest, err := os.ReadFile(filepath.Join(root, "node-digest"))
	if err != nil || string(digest) != npmCaptureHash(raw) {
		return fmt.Errorf("private physical node bytes changed")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	expected := "#!/bin/sh\nexec " + shquote.Quote(self) + " private-official-npm " + shquote.Quote(root) + " \"$@\"\n"
	npm, err := os.ReadFile(filepath.Join(root, "tools", "npm"))
	if err != nil || string(npm) != expected {
		return fmt.Errorf("private sibling npm adapter changed")
	}
	return nil
}

package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

var publishWheelPlatforms = []string{
	"manylinux_2_17_x86_64",
	"manylinux_2_17_aarch64",
	"musllinux_1_2_x86_64",
	"musllinux_1_2_aarch64",
	"macosx_10_9_x86_64",
	"macosx_11_0_arm64",
}

func validateWheelDir(dir, version string) ([]string, error) {
	if !releaseVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("invalid wheel version %q", version)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("wheel artifact directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("wheel artifact root must be a real directory")
	}
	expected := make(map[string]struct{}, len(publishWheelPlatforms))
	for _, platform := range publishWheelPlatforms {
		expected[fmt.Sprintf("yolo_jail-%s-py3-none-%s.whl", version, platform)] = struct{}{}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if _, ok := expected[name]; !ok {
			return nil, fmt.Errorf("unexpected wheel artifact %q", name)
		}
		fileInfo, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 || fileInfo.Size() <= 0 || fileInfo.Size() > 512<<20 {
			return nil, fmt.Errorf("wheel %q must be a non-empty regular file no larger than 512 MiB", name)
		}
		if err := validateWheel(filepath.Join(dir, name), version); err != nil {
			return nil, fmt.Errorf("wheel %q: %w", name, err)
		}
		actual = append(actual, name)
	}
	if len(actual) != len(expected) {
		return nil, fmt.Errorf("found %d wheel artifacts; want all %d release platforms", len(actual), len(expected))
	}
	sort.Strings(actual)
	return actual, nil
}

func validateWheel(filename, version string) error {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return fmt.Errorf("not a readable wheel ZIP: %w", err)
	}
	defer archive.Close()
	if len(archive.File) == 0 || len(archive.File) > 1000 {
		return errors.New("wheel has an invalid member count")
	}
	metadataName := "yolo_jail-" + version + ".dist-info/METADATA"
	required := map[string]struct{}{
		metadataName: {},
		"yolo_jail-" + version + ".dist-info/WHEEL":  {},
		"yolo_jail-" + version + ".dist-info/RECORD": {},
	}
	found := make(map[string]struct{}, len(required))
	members := make(map[string]bool, len(archive.File))
	var total uint64
	for _, item := range archive.File {
		name := item.Name
		memberPath := strings.TrimSuffix(name, "/")
		if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) || path.Clean(memberPath) != memberPath || memberPath == ".." || strings.HasPrefix(memberPath, "../") {
			return fmt.Errorf("unsafe wheel member path %q", name)
		}
		isDir := item.FileInfo().IsDir()
		if isDir {
			if !strings.HasSuffix(name, "/") {
				return fmt.Errorf("wheel directory member %q lacks its trailing slash", name)
			}
		} else if !item.Mode().IsRegular() {
			return fmt.Errorf("wheel member %q is not a regular file", name)
		}
		if _, duplicate := members[memberPath]; duplicate {
			return fmt.Errorf("duplicate wheel member %q", name)
		}
		for parent := path.Dir(memberPath); parent != "."; parent = path.Dir(parent) {
			if isDir, exists := members[parent]; exists && !isDir {
				return fmt.Errorf("wheel member %q conflicts with file %q", name, parent)
			}
		}
		if !isDir {
			prefix := memberPath + "/"
			for existing := range members {
				if strings.HasPrefix(existing, prefix) {
					return fmt.Errorf("wheel member %q conflicts with child member %q", name, existing)
				}
			}
		}
		members[memberPath] = isDir
		if item.UncompressedSize64 > 1<<30-total {
			return errors.New("wheel expands beyond the 1 GiB validation limit")
		}
		total += item.UncompressedSize64
		if _, expected := required[name]; expected {
			if !item.Mode().IsRegular() || item.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("wheel metadata member %q must be a regular file", name)
			}
			found[name] = struct{}{}
		}
		if name == metadataName {
			reader, err := item.Open()
			if err != nil {
				return err
			}
			data, readErr := io.ReadAll(io.LimitReader(reader, 1<<20))
			closeErr := reader.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			if !metadataMatches(data, version) {
				return errors.New("wheel METADATA package name or version does not match the dispatch")
			}
		}
	}
	if len(found) != len(required) {
		return fmt.Errorf("wheel is missing a required dist-info member")
	}
	return nil
}

func metadataMatches(data []byte, version string) bool {
	nameSeen, versionSeen := false, false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "Name: "):
			if nameSeen || strings.TrimSpace(strings.TrimPrefix(line, "Name: ")) != "yolo-jail" {
				return false
			}
			nameSeen = true
		case strings.HasPrefix(line, "Version: "):
			if versionSeen || strings.TrimSpace(strings.TrimPrefix(line, "Version: ")) != version {
				return false
			}
			versionSeen = true
		}
	}
	return nameSeen && versionSeen
}

func validateWheelsCLI(args []string, out io.Writer, errOut io.Writer) int {
	flags := flag.NewFlagSet("validate-wheels", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "dist", "downloaded wheel artifact directory")
	version := flags.String("version", "", "exact release version")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errOut, "validate-wheels: unexpected arguments")
		return 2
	}
	wheels, err := validateWheelDir(*dir, *version)
	if err != nil {
		fmt.Fprintf(errOut, "validate-wheels: %v\n", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(wheels); err != nil {
		fmt.Fprintf(errOut, "validate-wheels: write wheel list: %v\n", err)
		return 1
	}
	return 0
}

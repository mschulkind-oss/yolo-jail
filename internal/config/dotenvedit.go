package config

// dotenvedit.go edits one name's lines in a dotenv file for `yolo config secret|unsecret`
// (secret-env-scoping-plan.md SEP-D12): a line is moved verbatim, every line that sets the name
// leaves the source, and every write is a temp file renamed over the original in its directory,
// so a reader never sees a half-written file. A file keeps its mode; one created here is 0600.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// dotenvLineName is the name a dotenv line assigns, by ParseDotenv's rules; "" for a comment,
// a blank line or a malformed one.
func dotenvLineName(raw string) string {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimLeft(line[len("export "):], " \t\n\r\v\f")
	}
	key, _, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	key = strings.TrimSpace(key)
	if !envVarNameRe.MatchString(key) {
		return ""
	}
	return key
}

// DotenvLine is the last line of path that assigns name (the one ParseDotenv reads), without
// its line ending; found is false when none does or the file is missing.
func DotenvLine(path, name string) (line string, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	for _, raw := range strings.SplitAfter(string(data), "\n") {
		if dotenvLineName(raw) == name {
			line, found = strings.TrimRight(raw, "\r\n"), true
		}
	}
	return line, found, nil
}

// RemoveDotenvName removes every line of path that assigns name and returns the last of them
// (the one ParseDotenv reads); found is false, and nothing is written, when none does.
func RemoveDotenvName(path, name string) (line string, found bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	var kept strings.Builder
	for _, raw := range strings.SplitAfter(string(data), "\n") {
		if raw != "" && dotenvLineName(raw) == name {
			line, found = strings.TrimRight(raw, "\r\n"), true
			continue
		}
		kept.WriteString(raw)
	}
	if !found {
		return "", false, nil
	}
	return line, true, writeFileAtomic(path, []byte(kept.String()))
}

// AppendDotenvLine appends line to path, ending the file's last line first when it has no
// newline, and creates the file (0600) when it is missing.
func AppendDotenvLine(path, line string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return writeFileAtomic(path, []byte(text+line+"\n"))
}

// writeFileAtomic replaces path's bytes through a temp file in its directory, keeping the
// existing file's mode, or 0600 for a new one.
func writeFileAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

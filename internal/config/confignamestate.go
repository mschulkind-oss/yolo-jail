package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// configNameKind is what a config file name holds, as file selection sees it (RC-D59,
// docs/design/readable-config.md#RC-D59). Presence is os.Lstat, not os.Stat: a name that
// exists in any form is a candidate, so a link to nothing is never read as "no file".
type configNameKind int

const (
	nameAbsent configNameKind = iota
	nameRegular
	nameDangling
	nameLoop
	nameDirectory
	nameUnreadable
)

// configNameState classifies path. target is the link's own text (os.Readlink) when path is a
// symbolic link, since that is what a dotfiles manager shows; reason is the read failure's
// cause for nameUnreadable. A name whose followed target is neither a directory nor a regular
// file (a device, a fifo) is nameRegular: it is read as it always was.
func configNameState(path string) (kind configNameKind, target, reason string) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nameAbsent, "", ""
		}
		return nameUnreadable, "", readFailureReason(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		target, _ = os.Readlink(path)
		followed, statErr := os.Stat(path)
		switch {
		case statErr == nil:
			info = followed
		case errors.Is(statErr, syscall.ELOOP):
			return nameLoop, target, ""
		case errors.Is(statErr, fs.ErrNotExist) || errors.Is(statErr, syscall.ENOTDIR):
			return nameDangling, target, ""
		default:
			return nameUnreadable, target, readFailureReason(statErr)
		}
	}
	if info.IsDir() {
		return nameDirectory, target, ""
	}
	f, err := os.Open(path)
	if err != nil {
		return nameUnreadable, target, readFailureReason(err)
	}
	_ = f.Close()
	return nameRegular, target, ""
}

func readFailureReason(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "permission denied"
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// configNamePresent is whether file selection counts path as there: anything but absent. A
// present name that is not a readable regular file is refused by configNameRefusal.
func configNamePresent(path string) bool {
	kind, _, _ := configNameState(path)
	return kind != nameAbsent
}

// configNameRefusal is RC-D59's refusal for a config name that exists but cannot be read as a
// regular file, or nil for a name that is absent or readable. from, when not empty, is where the
// name came from (`config.d/work.toml`, `include_if_found[1] in config.jsonc`) and prefixes the
// message. The refusal carries its fact and step apart so `yolo check` FAILs with the same
// sentence and puts the step in its note.
func configNameRefusal(path, from string) *ConfigError {
	kind, target, reason := configNameState(path)
	if kind == nameAbsent || kind == nameRegular {
		return nil
	}
	return configNameError(path, from, kind, target, reason)
}

func configNameError(path, from string, kind configNameKind, target, reason string) *ConfigError {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	rm := "rm " + shquote.Quote(path)
	var fact, step, tail string
	switch kind {
	case nameDangling:
		fact = path + " is a symbolic link to " + target + ", which does not exist"
		step = "Restore the target (if a dotfiles manager keeps this file, sync it on this machine), or remove the link: " + rm + "."
		tail = " Nothing was started."
	case nameLoop:
		fact = path + " is a symbolic link that leads back to itself (" + target + ")"
		step = "Point it at the config file, or remove it: " + rm + "."
	case nameDirectory:
		fact = path + " is a directory, and a config file is expected there"
		step = "Move it aside, or put the config in " + path + "/" + filepath.Base(path) + " and point the name at it."
	default:
		fact = path + " cannot be read: " + reason
		step = "Fix its permissions (chmod u+r " + shquote.Quote(path) + "), or remove it."
	}
	prefix := ""
	if from != "" {
		prefix = from + ": "
	}
	return &ConfigError{
		Msg:  prefix + fact + ". " + step + tail,
		Fact: prefix + fact,
		Step: step,
	}
}

// ConfigNameRefusal is configNameRefusal for a reader outside this package (`yolo config
// migrate`'s discovery): nil for a name that is absent or a readable file, else the refusal.
func ConfigNameRefusal(path, from string) error {
	if refusal := configNameRefusal(path, from); refusal != nil {
		return refusal
	}
	return nil
}

// ConfigNamePresent is whether file selection counts path as there (any form, os.Lstat).
func ConfigNamePresent(path string) bool { return configNamePresent(path) }

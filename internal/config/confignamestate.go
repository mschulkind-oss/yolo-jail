package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// configNameKind is what a config file name holds, as file selection sees it (RC-D59,
// docs/reference/config/rulings.md#RC-D59). Presence is os.Lstat, not os.Stat: a name that
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
		if twinStep := danglingTwinStep(path, target, rm); twinStep != "" {
			step = twinStep
		}
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

// danglingTwinStep is LC-D11's step for a dangling link to a legacy config whose target has a
// `.toml` twin (docs/design/legacy-config-compat.md#LC-D11): the usual way a link comes to
// point at nothing is `yolo config migrate` run where the target lives, which renamed it. The
// twin is what the link should point at, under the TOML name, since a `.jsonc` name is read
// as JSONC. "" when the link or its target is not a legacy config name or no twin exists. The
// target is only read as the link's own text, never followed past the twin's existence check.
func danglingTwinStep(path, target, rm string) string {
	legacyExt := func(name string) string {
		for _, ext := range []string{".jsonc", ".json"} {
			if strings.HasSuffix(name, ext) {
				return ext
			}
		}
		return ""
	}
	linkExt, targetExt := legacyExt(path), legacyExt(target)
	if linkExt == "" || targetExt == "" {
		return ""
	}
	twinTarget := strings.TrimSuffix(target, targetExt) + ".toml"
	twin := twinTarget
	if !filepath.IsAbs(twin) {
		twin = filepath.Join(filepath.Dir(path), twin)
	}
	if info, err := os.Stat(twin); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	tomlName := strings.TrimSuffix(path, linkExt) + ".toml"
	if _, err := os.Lstat(tomlName); err == nil {
		return "Its target was converted to " + twin + ", and " + tomlName + " is there already: remove the stale link: " + rm + "."
	}
	return "Its target was converted to " + twin + ": replace the link with one at the TOML name: " + rm +
		" && ln -s " + shquote.Quote(twinTarget) + " " + shquote.Quote(tomlName) + "."
}

// includeSkipNotice is RC-D67's answer for an include_if_found entry whose name exists but cannot
// be read as a regular file: "include if found" is best effort, so such an entry counts as not
// found and is skipped, and the notice says once what was skipped and how to bring it back. ""
// for an entry that is absent (silently skipped) or readable (read).
func includeSkipNotice(path, from string) string {
	refusal := configNameRefusal(path, from)
	if refusal == nil {
		return ""
	}
	return refusal.Fact + includeSkipMarker + " To include it: " + refusal.Step
}

// includeSkipMarker is the clause every include-skip notice carries, so a reader that turns the
// loader's warnings into errors (a migration's strict read) can tell a skipped include, which is
// not a problem with the config, from one that is (isIncludeSkipNotice).
const includeSkipMarker = "; include_if_found is best effort, so it was skipped."

func isIncludeSkipNotice(msg string) bool { return strings.Contains(msg, includeSkipMarker) }

// IncludeSkipped is whether an include_if_found entry at path counts as not found (RC-D67):
// absent, or present but not readable as a regular file.
func IncludeSkipped(path string) bool {
	kind, _, _ := configNameState(path)
	return kind != nameRegular
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

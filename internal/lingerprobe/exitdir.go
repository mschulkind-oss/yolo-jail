package lingerprobe

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WHERE CONMON WRITES THE EXIT FILE. podman hands conmon `--exit-dir
// <engine tmp_dir>/exits`, and conmon writes `<exit-dir>/<full container id>`
// holding the exit status the moment the container's process is reaped — before
// podman's own `died` event, which is whichever podman process next syncs the
// container's state. The engine tmp_dir defaults to `/run/libpod` for a rootful
// podman and `$XDG_RUNTIME_DIR/libpod/tmp` for a rootless one, and containers.conf
// may move it (`[engine] tmp_dir`).
//
// Detection is DEFENSIVE rather than exact: podman's default for a rootless user
// without XDG_RUNTIME_DIR falls through `/run/user/<uid>` to a tmp directory, and a
// containers.conf override may name any path. Every candidate is tried in order
// and the first that is an existing directory wins; none existing is an answer
// ("no_exit_dir") rather than a guess. podman creates the directory when its
// runtime initializes, so by the time a container is running it exists.

// ExitDirCandidates lists the directories conmon may write exit files into,
// most specific first. euid 0 is rootful podman; anything else is rootless.
func ExitDirCandidates(euid int, getenv func(string) string, confTmpDir string) []string {
	var out []string
	if confTmpDir != "" {
		out = append(out, filepath.Join(confTmpDir, "exits"))
	}
	if euid == 0 {
		return append(out, "/run/libpod/exits")
	}
	uid := strconv.Itoa(euid)
	if x := getenv("XDG_RUNTIME_DIR"); x != "" {
		out = append(out, filepath.Join(x, "libpod", "tmp", "exits"))
	}
	out = append(out, filepath.Join("/run/user", uid, "libpod", "tmp", "exits"))
	tmp := getenv("TMPDIR")
	if tmp == "" {
		tmp = "/tmp"
	}
	return append(out, filepath.Join(tmp, "podman-run-"+uid, "libpod", "tmp", "exits"))
}

// DetectExitDir picks the first existing candidate.
func DetectExitDir(euid int, getenv func(string) string) (string, bool) {
	conf := ConfiguredTmpDir(containersConfFiles(euid, getenv), euid, getenv)
	for _, d := range ExitDirCandidates(euid, getenv, conf) {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d, true
		}
	}
	return "", false
}

// containersConfFiles is the containers.conf search list, LOWEST priority
// first (a later file's setting wins). CONTAINERS_CONF replaces the list.
func containersConfFiles(euid int, getenv func(string) string) []string {
	if c := getenv("CONTAINERS_CONF"); c != "" {
		return []string{c}
	}
	files := []string{"/usr/share/containers/containers.conf", "/etc/containers/containers.conf"}
	if euid != 0 {
		cfg := getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			if h := getenv("HOME"); h != "" {
				cfg = filepath.Join(h, ".config")
			}
		}
		if cfg != "" {
			files = append(files, filepath.Join(cfg, "containers", "containers.conf"))
		}
	}
	return files
}

// ConfiguredTmpDir is the last `[engine] tmp_dir` set across files, with
// $UID/$HOME-style references expanded; "" when none sets it. A line scan, not
// a TOML parser: the one key this needs is a quoted string on its own line.
func ConfiguredTmpDir(files []string, euid int, getenv func(string) string) string {
	found := ""
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		section := ""
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if i := strings.IndexByte(line, '#'); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			if strings.HasPrefix(line, "[") {
				section = strings.Trim(line, "[] ")
				continue
			}
			if section != "engine" {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(k) != "tmp_dir" {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			found = os.Expand(v, func(name string) string {
				if name == "UID" {
					return strconv.Itoa(euid)
				}
				return getenv(name)
			})
		}
		_ = fh.Close()
	}
	return found
}

// matchesExitFile reports whether an exit-dir entry is THIS container's exit
// file. id may be the 12-character short id `podman ps -q` prints; the file is
// named by the full id. A name with a dot is a temporary file conmon renames
// into place (glib's g_file_set_contents writes `<name>.XXXXXX` first).
func matchesExitFile(name, id string) bool {
	return len(id) >= 12 && strings.HasPrefix(name, id) && !strings.ContainsRune(name, '.')
}

package run

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// THE AMD CDI SPEC PROBE. In `gpu.mode: "cdi"` the launch emits `--device amd.com/gpu=all`, and
// podman resolves that through its CDI registry, which reads the spec dirs the way
// tags.cncf.io/container-device-interface does:
//
//   - every regular file DIRECTLY in a spec dir whose extension is `.json` or `.yaml`, whatever
//     its name (no `.yml`, no subdirectories);
//   - each recognized by the `kind` its content declares, so `amd.com/gpu=all` comes from any
//     spec of kind `amd.com/gpu`;
//   - the spec dirs being `/etc/cdi` and `/var/run/cdi` unless containers.conf's
//     `[engine] cdi_spec_dirs` names others.
//
// The probe reads the same way, because a narrower reader is a regression: G28's first fix
// looked for exactly /etc/cdi/amd.json and /var/run/cdi/amd.json, so a host whose spec was
// amd.yaml, or lived in a configured dir, lost a GPU podman would have given it — and the
// warning's advice then wrote a second spec of the same kind, which the registry rejects as a
// conflict. Where the probe cannot know which dirs podman reads, it reads MORE: every
// containers.conf it can find (system, drop-ins, the user's, CONTAINERS_CONF and its override)
// adds its dirs to the defaults rather than replacing them. A wider reader can only fail toward
// the pre-G28 behavior (the flag is emitted and podman decides); a narrower one silently takes
// the GPU away.
//
// The launch's probe (rocmHostAvailable) and `yolo check`'s AMD section both call
// FindAMDCDISpec, because the two disagreeing is G28 (docs/plans/setup-support-gaps.md).

// AMDCDIKind is the CDI kind `--device amd.com/gpu=…` names.
const AMDCDIKind = "amd.com/gpu"

// AMDCDIDefaultSpecDirs are the CDI registry's spec dirs when containers.conf names none.
var AMDCDIDefaultSpecDirs = []string{"/etc/cdi", "/var/run/cdi"}

// AMDCDISpecGenerate is the command that writes an AMD spec. Both readers name it as the next
// step when FindAMDCDISpec finds none, which is also when writing it cannot conflict with an
// existing spec of the same kind.
const AMDCDISpecGenerate = "sudo amd-ctk cdi generate --output=/etc/cdi/amd.json"

// cdiRoot is the machine root the launch's probe reads spec dirs and containers.conf under: /
// in production. A variable only so a test reads a tree it made; production never assigns it.
var cdiRoot = "/"

// FindAMDCDISpec returns the host path of the first AMD CDI spec in the spec dirs, or "" when
// there is none, and the spec dirs it searched (host paths, defaults first). root is the machine
// root every path is read under ("/" in production); getenv supplies HOME, XDG_CONFIG_HOME,
// CONTAINERS_CONF and CONTAINERS_CONF_OVERRIDE, and nil means os.Getenv.
func FindAMDCDISpec(root string, getenv func(string) string) (string, []string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	searched := cdiSpecDirs(root, getenv)
	for _, dir := range searched {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if ext := filepath.Ext(e.Name()); ext != ".json" && ext != ".yaml" {
				continue
			}
			// A file this process cannot read is one a rootless podman running as this user
			// cannot read either, so skipping it is the registry's answer too.
			data, err := os.ReadFile(filepath.Join(root, dir, e.Name()))
			if err != nil {
				continue
			}
			if cdiSpecKind(data) == AMDCDIKind {
				return filepath.Join(dir, e.Name()), searched
			}
		}
	}
	return "", searched
}

// cdiSpecDirs is AMDCDIDefaultSpecDirs plus every `[engine] cdi_spec_dirs` entry the
// containers.conf files under root name, in order, without duplicates.
func cdiSpecDirs(root string, getenv func(string) string) []string {
	dirs := append([]string(nil), AMDCDIDefaultSpecDirs...)
	seen := map[string]bool{}
	for _, d := range dirs {
		seen[d] = true
	}
	for _, f := range containersConfFiles(root, getenv) {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		var conf struct {
			Engine struct {
				CDISpecDirs []string `toml:"cdi_spec_dirs"`
			} `toml:"engine"`
		}
		// A file podman cannot parse stops podman before any CDI lookup, so it adds nothing.
		if _, err := toml.Decode(string(data), &conf); err != nil {
			continue
		}
		for _, d := range conf.Engine.CDISpecDirs {
			if d = filepath.Clean(d); filepath.IsAbs(d) && !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
	}
	return dirs
}

// containersConfFiles lists every containers.conf podman may read, as host paths: the system
// and user files, their drop-in dirs (including the rootful/rootless ones and a rootless
// per-uid subdir), and the two environment overrides. Rootful and rootless podman read
// different subsets; the probe reads the union (see the file comment).
func containersConfFiles(root string, getenv func(string) string) []string {
	var files []string
	for _, k := range []string{"CONTAINERS_CONF", "CONTAINERS_CONF_OVERRIDE"} {
		if v := getenv(k); filepath.IsAbs(v) {
			files = append(files, v)
		}
	}
	bases := []string{"/usr/share/containers", "/etc/containers"}
	cfg := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(cfg) {
		cfg = ""
		if h := getenv("HOME"); filepath.IsAbs(h) {
			cfg = filepath.Join(h, ".config")
		}
	}
	if cfg != "" {
		bases = append(bases, filepath.Join(cfg, "containers"))
	}
	for _, b := range bases {
		files = append(files, filepath.Join(b, "containers.conf"))
		for _, pattern := range []string{
			"containers.conf.d/*.conf",
			"containers.rootful.conf.d/*.conf",
			"containers.rootless.conf.d/*.conf",
			"containers.rootless.conf.d/*/*.conf",
		} {
			matches, _ := filepath.Glob(filepath.Join(root, b, pattern))
			for _, m := range matches {
				if rel, err := filepath.Rel(root, m); err == nil {
					files = append(files, "/"+filepath.ToSlash(rel))
				}
			}
		}
	}
	return files
}

// cdiSpecKind is the top-level `kind` a CDI spec declares, "" when none is found. The registry
// decodes both extensions as YAML, of which JSON is a subset, so a JSON body is read as JSON
// whatever the extension, and anything else by its unindented `kind:` line — the one key this
// needs, which no CDI writer emits in any other shape.
func cdiSpecKind(data []byte) string {
	var spec struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &spec); err == nil {
		return spec.Kind
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "kind:") {
			continue
		}
		v := strings.TrimPrefix(line, "kind:")
		if i := strings.Index(v, " #"); i >= 0 {
			v = v[:i]
		}
		return strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return ""
}

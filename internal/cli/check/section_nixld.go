package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
)

// sectionNixLD runs the "FHS loader (nix-ld)" block — an in-jail smoke test
// that the FHS ELF interpreter wiring resolves FHS binaries both env-free and
// under the jail's actual environment, and checks mise-installed binaries for
// glibc interpreter skew against the merged tree's libc.
//
// Skipped entirely on the host (the mise node + /lib64 wiring are jail state)
// and when no mise node is installed (nothing to probe — not a failure).
func (o *Options) sectionNixLD(r *reporter) {
	if !o.inJail() {
		return // jail-only wiring; nothing to probe on the host
	}
	node := o.MiseNode()
	if node == "" {
		return // no mise node installed — nothing this tripwire covers
	}
	r.sectionHeader("FHS loader (nix-ld)")
	// 1. Env-free probe: ensures nix-ld defaults resolve without ambient environment
	res := o.Exec([]string{"env", "-i", node, "--version"}, "", nil, 15*time.Second)
	switch {
	case res.Timeout:
		r.fail("mise node env-free probe timed out", probeNote("env", "-i", node, "--version"))
	case !res.Ran:
		r.warn("could not run the mise node probe: exec failed", probeNote("env", "-i", node, "--version"))
	case res.RC == 0 && strings.HasPrefix(strings.TrimSpace(res.Stdout), "v"):
		r.ok("mise node runs env-free: " + strings.TrimSpace(res.Stdout) + " (nix-ld OK)")
	default:
		detail := firstLine(strings.TrimSpace(res.Stderr))
		if detail == "" {
			detail = firstLine(strings.TrimSpace(res.Stdout))
		}
		r.fail("mise node fails under a scrubbed environment: "+detail,
			"The FHS loader (nix-ld) wiring has regressed — an FHS binary can no "+
				"longer find libstdc++ without LD_LIBRARY_PATH. Check the /lib64 "+
				"interpreter symlink and the baked /usr/share/nix-ld/lib dir "+
				"(flake.nix); see docs/reference/mise-node-dynamic-linking.md.")
	}

	// 2. Ambient environment probe: ensures the jail's actual environment does not break dynamic loader resolution
	resAmbient := o.Exec([]string{node, "--version"}, "", nil, 15*time.Second)
	switch {
	case resAmbient.Timeout:
		r.fail("mise node ambient probe timed out", probeNote(node, "--version"))
	case !resAmbient.Ran:
		r.warn("could not run the mise node ambient probe: exec failed", probeNote(node, "--version"))
	case resAmbient.RC != 0:
		detail := firstLine(strings.TrimSpace(resAmbient.Stderr))
		if detail == "" {
			detail = firstLine(strings.TrimSpace(resAmbient.Stdout))
		}
		r.fail("mise node fails under the jail's actual environment: "+detail,
			"The jail's environment (e.g. LD_LIBRARY_PATH) broke dynamic loader resolution.")
	}

	// 3. Probe representative mise-installed binaries for glibc interpreter skew against merged tree
	mergedGlibc := ""
	if o.MergedGlibc != nil {
		mergedGlibc = o.MergedGlibc()
	}
	if o.MiseBinaries != nil {
		warned := map[string]struct{}{}
		for _, bin := range o.MiseBinaries() {
			interp, err := hostfloor.ElfInterp(bin)
			if err != nil || interp == "" {
				continue
			}
			binGlibc := nixStorePackageDir(interp)
			if binGlibc == "" {
				// FHS binary (e.g. /lib64/ld-linux-*.so.2 via nix-ld)
				continue
			}
			binName := filepath.Base(bin)
			if mergedGlibc != "" && binGlibc != mergedGlibc {
				warnKey := binName + ":" + binGlibc
				if _, ok := warned[warnKey]; !ok {
					warned[warnKey] = struct{}{}
					r.warn(fmt.Sprintf("mise %s interpreter glibc differs from merged tree: %s (tool) vs %s (image)",
						binName, nixStorePackageLabel(binGlibc), nixStorePackageLabel(mergedGlibc)),
						fmt.Sprintf("Tool %s was built against %s, but the jail image provides %s. "+
							"If ambient LD_LIBRARY_PATH exports /lib or /usr/lib, this causes GLIBC_PRIVATE symbol lookup errors. "+
							"Reinstall the tool under the current image to align the interpreter.",
							bin, binGlibc, mergedGlibc))
				}
			}

			// Probe execution in ambient environment
			resTool := o.Exec([]string{bin, "--version"}, "", nil, 15*time.Second)
			if resTool.Ran && resTool.RC != 0 && (strings.Contains(resTool.Stderr, "GLIBC_PRIVATE") || strings.Contains(resTool.Stderr, "symbol lookup error")) {
				detail := firstLine(strings.TrimSpace(resTool.Stderr))
				r.fail(fmt.Sprintf("mise %s fails in jail environment: %s", binName, detail),
					"The tool failed to execute because dynamic library lookup resolved an incompatible glibc.")
			}
		}
	}
	r.blank()
}

// realFirstMiseNode returns the path to a mise-installed node binary, or "" if
// none is present. It globs /mise/installs/node/*/bin/node and returns the first
// match — the probe only needs one FHS node to exercise the loader.
func realFirstMiseNode() string {
	matches, err := filepath.Glob("/mise/installs/node/*/bin/node")
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// realMergedGlibc returns the store path of the merged tree's glibc (e.g. from /lib/libc.so.6).
func realMergedGlibc() string {
	for _, p := range []string{"/lib/libc.so.6", "/usr/lib/libc.so.6"} {
		target, err := filepath.EvalSymlinks(p)
		if err == nil {
			if dir := nixStorePackageDir(target); dir != "" {
				return dir
			}
		}
	}
	return ""
}

// nixStorePackageDir extracts /nix/store/<hash>-<name> from a store path.
func nixStorePackageDir(p string) string {
	clean := filepath.Clean(p)
	if !strings.HasPrefix(clean, "/nix/store/") {
		return ""
	}
	rel := strings.TrimPrefix(clean, "/nix/store/")
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return filepath.Join("/nix/store", parts[0])
}

// nixStorePackageLabel strips the hash from /nix/store/<hash>-<name> returning <name>.
func nixStorePackageLabel(storeDir string) string {
	base := filepath.Base(storeDir)
	if idx := strings.IndexByte(base, '-'); idx > 0 && idx < len(base)-1 {
		return base[idx+1:]
	}
	return base
}

// realMiseBinaries finds representative executable binaries in /mise/installs.
func realMiseBinaries() []string {
	var matches []string
	if m, err := filepath.Glob("/mise/installs/*/*/bin/*"); err == nil {
		matches = append(matches, m...)
	}
	if m, err := filepath.Glob("/mise/installs/*/*/*"); err == nil {
		matches = append(matches, m...)
	}
	var out []string
	seen := map[string]struct{}{}
	for _, m := range matches {
		real, err := filepath.EvalSymlinks(m)
		if err != nil {
			continue
		}
		if _, ok := seen[real]; ok {
			continue
		}
		seen[real] = struct{}{}
		fi, err := os.Stat(real)
		if err != nil || fi.IsDir() || fi.Mode()&0111 == 0 {
			continue
		}
		out = append(out, real)
	}
	return out
}

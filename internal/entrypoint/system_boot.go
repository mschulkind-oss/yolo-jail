package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// tzRunDir the writable tmpfs (/run) that backs
// the image's /etc/localtime + /etc/timezone symlinks (root fs is read-only).
// A package var so tests can redirect it to a temp dir.
var tzRunDir = "/run"

// configureTimezone populate /run/localtime
// and /run/timezone from $TZ so anything reading /etc/localtime directly (Go's
// time pkg, some Java/Ruby paths, `date` after `env -i`) agrees with the host
// wall clock. Best-effort: unset $TZ or an unresolvable zone file leaves the
// dangling symlinks alone (callers fall back to UTC, matching pre-fix behavior).
func configureTimezone(e *Env) {
	tz := e.Getenv("TZ")
	if tz == "" {
		return
	}
	tzdir := e.Getenv("TZDIR")
	if tzdir == "" {
		tzdir = "/usr/share/zoneinfo"
	}
	zoneFile := filepath.Join(tzdir, tz)
	// Require a regular file (following symlinks).
	if fi, err := os.Stat(zoneFile); err != nil || !fi.Mode().IsRegular() {
		return
	}
	runDir := tzRunDir
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return
	}
	localtime := filepath.Join(runDir, "localtime")
	// Remove any existing localtime (symlink or regular file); ignore ENOENT.
	if _, err := os.Lstat(localtime); err == nil {
		if err := os.Remove(localtime); err != nil {
			return
		}
	}
	if err := os.Symlink(zoneFile, localtime); err != nil {
		return
	}
	// (run_dir / "timezone").write_text(f"{tz}\n") — in-place write.
	_ = os.WriteFile(filepath.Join(runDir, "timezone"), []byte(tz+"\n"), 0o644)
}

// generateLdCache populate /run/ld.so.cache
// (target of the image's /etc/ld.so.cache symlink) from the /lib + /usr/lib
// farm. Generation runs here rather than at image build time because the farm
// derivation builds natively on darwin for macOS hosts, where the Linux
// ldconfig binary cannot run. Best-effort — a diagnostics gap, not a startup
// error, when ldconfig is missing or fails.
//
// extraDirs are scanned IN ADDITION to /etc/ld.so.conf's entries, which is ldconfig's own
// semantics for directories named on the command line. It exists for one caller: the
// store-package farm (C4/C5), whose lib dir is on the /run tmpfs and therefore cannot be
// listed in the baked /etc/ld.so.conf. A dir that does not exist is dropped here rather
// than handed to ldconfig, which would warn about it on every boot of every jail that
// does not opt in. Note what this cache is and is not: flake.nix's own comment records
// that nixpkgs' ld.so never reads /etc/ld.so.cache, so this serves `ldconfig -p` and
// other cache readers — RUNTIME discovery of a store-delivered library is
// LD_LIBRARY_PATH's job, which GenerateStorePackages sets.
func generateLdCache(extraDirs ...string) {
	ldconfig, err := exec.LookPath("ldconfig")
	if err != nil {
		return
	}
	argv := []string{"-C", "/run/ld.so.cache", "-f", "/etc/ld.so.conf"}
	for _, d := range extraDirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			argv = append(argv, d)
		}
	}
	cmd := exec.Command(ldconfig, argv...)
	// capture_output=True: discard stdout/stderr.
	cmd.Stdout = nil
	cmd.Stderr = nil
	// timeout=30: enforce via a timer that kills the process.
	if err := cmd.Start(); err != nil {
		return
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

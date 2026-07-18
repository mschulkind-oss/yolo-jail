package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// tzRunDir mirrors entrypoint.TZ_RUN_DIR: the writable tmpfs (/run) that backs
// the image's /etc/localtime + /etc/timezone symlinks (root fs is read-only).
// A package var so tests can redirect it to a temp dir.
var tzRunDir = "/run"

// configureTimezone mirrors system.configure_timezone: populate /run/localtime
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
	// Python: zone_file.is_file() — a regular file (follows symlinks).
	if fi, err := os.Stat(zoneFile); err != nil || !fi.Mode().IsRegular() {
		return
	}
	runDir := tzRunDir
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return
	}
	localtime := filepath.Join(runDir, "localtime")
	// Python: if localtime.is_symlink() or localtime.exists(): unlink().
	// os.Remove drops either a symlink or a regular file; ignore ENOENT.
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

// generateLdCache mirrors system.generate_ld_cache: populate /run/ld.so.cache
// (target of the image's /etc/ld.so.cache symlink) from the /lib + /usr/lib
// farm. Generation runs here rather than at image build time because the farm
// derivation builds natively on darwin for macOS hosts, where the Linux
// ldconfig binary cannot run. Best-effort — a diagnostics gap, not a startup
// error, when ldconfig is missing or fails.
func generateLdCache() {
	ldconfig, err := exec.LookPath("ldconfig")
	if err != nil {
		return
	}
	cmd := exec.Command(ldconfig, "-C", "/run/ld.so.cache", "-f", "/etc/ld.so.conf")
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

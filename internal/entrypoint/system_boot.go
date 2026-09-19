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
	//
	// REPORTED, because by this point the symlink already succeeded: a jail whose
	// /etc/localtime says one zone and whose /etc/timezone says nothing is a
	// HALF-applied timezone, and the readers of the two files disagree from then on.
	// The earlier returns above are the genuinely empty cases (no $TZ, no zone file)
	// and stay silent — nothing was applied, so there is nothing to have diverged.
	if err := os.WriteFile(filepath.Join(runDir, "timezone"), []byte(tz+"\n"), 0o644); err != nil {
		e.warn("Warning: set timezone: /etc/localtime now names " + tz +
			" but the matching /etc/timezone could not be written: " + err.Error() +
			"; tools that read the name rather than the zone file will disagree")
	}
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
// ldCacheTimeout is the bound on the ldconfig run. It is the ONE wait on the boot
// path that a user can pay in full and never be told about — thirty seconds of
// launch, spent on a cache, attributed to nothing — which is why it goes through
// runBoundedStep rather than a second private copy of the same select. A var so a
// test can reach the timeout branch without sleeping for it.
var ldCacheTimeout = 30 * time.Second

func generateLdCache(e *Env, extraDirs ...string) {
	ldconfig, err := exec.LookPath("ldconfig")
	if err != nil {
		// A genuinely absent tool, not a failure: the image may not ship ldconfig at
		// all (macos-user bakes nothing). The log records that the step ran and found
		// nothing, so "ld.so.cache is stale" is answerable later without guessing
		// whether this code even executed.
		e.note("ld.so.cache: skipped, no ldconfig on PATH")
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
	runBoundedStep(e, "ldconfig (populate /run/ld.so.cache)", ldCacheTimeout, cmd)
}

// configureScratchPermissions ensures /tmp and /var/tmp are mode 01777 (sticky + world-writable).
// On container backends where /tmp is backed by an anonymous volume (-v /tmp), podman initializes
// permissions from the image's /tmp, which Nix derivations set to 0555 (read-only). Processes
// that drop capabilities (such as Chromium child processes) cannot write to mode 0555 directories.
//
// A FAILED CHMOD IS REPORTED, because its symptom lands nowhere near here: the jail
// boots clean, and the consequence surfaces much later as a capability-dropping
// child process — Chromium's renderers, the case this exists for — failing to write
// a temp file. That is the "it just does not work, with no trace of why" shape, and
// naming the directory at boot is the whole difference between a two-minute
// diagnosis and an afternoon.
func configureScratchPermissions(e *Env) {
	configureScratchPermissionsDirs(e, "/tmp", "/var/tmp")
}

// scratchChmod is os.Chmod, indirected only so a test can exercise the report. The
// jail's boot runs as root, where a chmod of a directory the caller owns does not
// fail, so the failure this reports has no portable way to be provoked for real.
var scratchChmod = os.Chmod

func configureScratchPermissionsDirs(e *Env, dirs ...string) {
	for _, dir := range dirs {
		// An absent dir stays silent: /var/tmp is legitimately missing on some
		// backends, and there is no permission to have got wrong on a directory that
		// does not exist.
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			if err := scratchChmod(dir, os.ModeSticky|0o777); err != nil {
				e.warn("Warning: could not make " + dir + " world-writable (mode 1777): " +
					err.Error() + "; processes that drop capabilities (Chromium's child " +
					"processes are the usual case) will fail to write scratch files there")
			}
		}
	}
}

package svcendpoint

// readgrowth_test.go covers the half of Read's size ceiling that a truthful stat can
// never reach.
//
// readEndpointFile enforces MaxEndpointFileSize TWICE — once on the size the stat
// declared, and once on the bytes that actually came back — because the stat is a
// snapshot and the file it described can be a different file by the time open(2)
// returns. readguard_test.go's TestReadRefusesAnOversizedFile exercises the first
// half only: it writes a sparse file whose declared size is already over the ceiling,
// so the read is never reached. MEASURED 2026-08-18 by mutation: deleting BOTH the
// io.LimitReader bound and the post-read check left every test in this package green,
// and the OOM the cap exists to stop was back — an unbounded slurp in PID 1 during
// boot, or in the agent's OAuth terminator mid-session.
//
// What was missing was a file whose stat under-reports it, and Linux provides one
// rather than the test having to win a race for it: every /proc file is a regular
// file with st_size 0 and arbitrary contents. A process re-exec'd with a megabyte of
// environment therefore has a /proc/self/environ that passes both stat gates and then
// hands back more than the ceiling — the exact shape "the file grew between the stat
// and the read" produces, without any timing to lose.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// statUnderReportChildEnv routes a re-exec of this test binary into the child half
// of the test below. A subprocess is unavoidable: the size of /proc/self/environ is
// fixed at execve and cannot be grown from inside the process that owns it, and
// another process's environ is unreadable without CAP_SYS_PTRACE — which this jail
// does not have, so reading a child's copy fails with EACCES even as uid 0. The
// process that reads it has to be the process that was exec'd with it.
const statUnderReportChildEnv = "YOLO_SVCENDPOINT_STAT_UNDERREPORT_CHILD"

// statUnderReportEnvBytes is how much environment the child is given. It must clear
// MaxEndpointFileSize (1 MiB) with room to spare, and stay well under the kernel's
// total limit for execve — min(RLIMIT_STACK/4, …), which is 2 MiB at the default 8 MiB
// stack. Twelve strings because MAX_ARG_STRLEN caps a SINGLE one at 128 KiB.
const (
	statUnderReportVars     = 12
	statUnderReportVarBytes = 100_000
)

// TestReadBoundsAFileTheStatUnderReports is one test with two halves: the parent
// builds the fixture by re-exec'ing this binary, and the child — the only process
// that can see it — does the asserting. A failure in the child fails its own subtest
// and the parent reports its whole output, so the diagnosis arrives by name either
// way.
func TestReadBoundsAFileTheStatUnderReports(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture is /proc/self/environ, a kernel-provided regular file whose " +
			"st_size is 0 and whose contents are not; no other platform has one, and the " +
			"alternative — racing a writer against the stat — is not a test, it is a coin flip")
	}
	if os.Getenv(statUnderReportChildEnv) == "1" {
		assertReadIsBoundedByWhatItRead(t)
		return
	}

	env := []string{statUnderReportChildEnv + "=1"}
	for i := 0; i < statUnderReportVars; i++ {
		env = append(env, fmt.Sprintf("YOLO_SVCENDPOINT_PAD_%02d=%s",
			i, strings.Repeat("x", statUnderReportVarBytes)))
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the child half of this test failed (%v). Its output:\n%s", err, out)
	}
	// Guard the guard: a child that SKIPPED — because the fixture no longer has the
	// property, or because the re-exec landed somewhere that ran no test at all —
	// exits 0 and would leave this passing while covering nothing.
	if !strings.Contains(string(out), "--- PASS: "+t.Name()) {
		t.Fatalf("the child exited 0 without reporting a pass; it never ran the assertions.\n%s", out)
	}
}

// assertReadIsBoundedByWhatItRead runs INSIDE the re-exec'd child, where
// /proc/self/environ is over a megabyte of NUL-separated strings and stat says zero.
func assertReadIsBoundedByWhatItRead(t *testing.T) {
	t.Helper()
	const path = "/proc/self/environ"

	// The fixture's premise, asserted rather than assumed — and FATAL rather than
	// skipped, because both halves of it are things a kernel could change under us
	// and the failure mode of not noticing is a test that runs forever and proves
	// nothing.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("fixture drift: %s is not readable here: %v", path, err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("fixture drift: %s is %s, so Read refuses it for a different reason "+
			"entirely and the size gate is never reached", path, fi.Mode())
	}
	if fi.Size() > MaxEndpointFileSize {
		t.Fatalf("fixture drift: %s now declares %d bytes, so the STAT gate refuses it and "+
			"this test has become a duplicate of TestReadRefusesAnOversizedFile",
			path, fi.Size())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture drift: reading %s: %v", path, err)
	}
	if len(raw) <= MaxEndpointFileSize {
		t.Fatalf("fixture drift: %s carries only %d bytes; the child was not given enough "+
			"environment to exceed the %d-byte ceiling (execve may have truncated it)",
			path, len(raw), MaxEndpointFileSize)
	}

	_, err = Read(path)
	if !errors.Is(err, ErrEndpointMalformed) {
		t.Fatalf("Read(%s) error = %v, want ErrEndpointMalformed", path, err)
	}
	// The DISCRIMINATING assertion. Without the bound, Read slurps the whole thing and
	// Parse rejects it anyway — NUL is not whitespace, so a megabyte of environment is
	// one field — which is the same sentinel, the same package, and none of the
	// protection. Only the ceiling's own message names the number.
	if want := strconv.Itoa(MaxEndpointFileSize); !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the %s-byte ceiling — the file was read in full and "+
			"then disliked by Parse, which is the unbounded read this cap exists to stop",
			err, want)
	}
}

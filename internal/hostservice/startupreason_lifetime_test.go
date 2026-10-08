package hostservice

import (
	"errors"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unicode"
)

// fdFlags returns fd's descriptor flags, or the errno fcntl returned.
func fdFlags(fd int) (int, error) {
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if errno != 0 {
		return 0, errno
	}
	return int(flags), nil
}

// inheritedStartupReasonChannel hands this process a channel shaped like the one a daemon
// inherits: a child end at a plain (inheritable) fd, and the three variables naming it.
func inheritedStartupReasonChannel(t *testing.T) (parentRead func() StartupReasonReadOutcome, fd int, attempt string) {
	t.Helper()
	parent, child, attempt, err := NewStartupReasonChannel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	fd, err = syscall.Dup(int(child.Fd()))
	_ = child.Close()
	if err != nil {
		t.Fatal(err)
	}
	if flags, err := fdFlags(fd); err != nil || flags&syscall.FD_CLOEXEC != 0 {
		t.Fatalf("fixture fd is not inheritable: flags=%d err=%v", flags, err)
	}
	t.Setenv(StartupReasonFDEnv, strconv.Itoa(fd))
	t.Setenv(StartupReasonAttemptEnv, attempt)
	t.Setenv(StartupReasonServiceEnv, "fixture")
	return func() StartupReasonReadOutcome {
		return ReadStartupReasonOutcome(nil, parent, "fixture", attempt, time.Now().Add(2*time.Second))
	}, fd, attempt
}

func assertStartupReasonEnvGone(t *testing.T) {
	t.Helper()
	for _, name := range []string{StartupReasonFDEnv, StartupReasonAttemptEnv, StartupReasonServiceEnv} {
		if v, set := os.LookupEnv(name); set {
			t.Errorf("%s is still set (%q), so the daemon's descendants inherit it", name, v)
		}
	}
}

func assertFDClosed(t *testing.T, fd int) {
	t.Helper()
	if _, err := fdFlags(fd); !errors.Is(err, syscall.EBADF) {
		t.Errorf("startup-reason fd %d is still open (fcntl err=%v)", fd, err)
	}
}

func TestProtectStartupReasonMarksTheInheritedFDCloseOnExec(t *testing.T) {
	_, fd, _ := inheritedStartupReasonChannel(t)
	t.Cleanup(func() { _ = syscall.Close(fd) })
	ProtectStartupReason()
	flags, err := fdFlags(fd)
	if err != nil || flags&syscall.FD_CLOEXEC == 0 {
		t.Fatalf("protected fd flags=%d err=%v, want FD_CLOEXEC set", flags, err)
	}
	if os.Getenv(StartupReasonFDEnv) == "" {
		t.Fatal("protecting the channel unset it before the daemon could write its refusal")
	}
}

func TestWriteStartupReasonClosesTheChannelAndUnsetsItsEnvironment(t *testing.T) {
	read, fd, _ := inheritedStartupReasonChannel(t)
	if err := WriteStartupReasonFromEnv(StartupReason{Class: "configuration", Reason: "refused"}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Kind != StartupReasonReadRecord || got.Reason != "refused" {
		t.Fatalf("parent read = %+v, want the one record", got)
	}
	assertFDClosed(t, fd)
	assertStartupReasonEnvGone(t)
	if err := WriteStartupReasonFromEnv(StartupReason{Class: "configuration", Reason: "again"}); err == nil {
		t.Fatal("a second write succeeded after the channel was spent")
	}
}

func TestReleaseStartupReasonClosesWithoutWriting(t *testing.T) {
	read, fd, _ := inheritedStartupReasonChannel(t)
	ReleaseStartupReason()
	assertFDClosed(t, fd)
	assertStartupReasonEnvGone(t)
	if got := read(); got.Kind != StartupReasonReadNoRecord {
		t.Fatalf("parent read after release = %+v, want no record (EOF)", got)
	}
	ReleaseStartupReason() // idempotent: no variables left, so no fd is closed twice
}

func TestStartupTextSanitizersReplaceFormatCharacters(t *testing.T) {
	const input = "safe‮evil​hidden⁦iso⁩end"
	for name, sanitize := range map[string]func(string) string{
		"safeStartupText": safeStartupText,
		"SafeCommandText": SafeCommandText,
	} {
		got := sanitize(input)
		for _, r := range got {
			if unicode.Is(unicode.Cf, r) {
				t.Errorf("%s(%q) = %q kept format character %U", name, input, got, r)
			}
		}
		if got != "safe evil hidden iso end" {
			t.Errorf("%s(%q) = %q, want format characters collapsed to spaces", name, input, got)
		}
	}
}

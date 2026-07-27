package entrypoint

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A4: writeExecutable must produce an executable file REGARDLESS of the process
// umask. The old code OR-ed owner-execute onto whatever mode the file happened to
// get from os.WriteFile — but WriteFile's perm is masked by umask, so under
// `umask 077` a "0o644" write yields 0o600 and the OR produced 0o700: no group or
// other read, on a file meant to be 0o744+. It only ever worked because the jail's
// umask is 0022.
func TestWriteExecutableIsUmaskIndependent(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	path := filepath.Join(t.TempDir(), "script.sh")
	if err := writeExecutable(path, "#!/bin/sh\necho hi\n"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o755 {
		t.Errorf("mode = %o, want 755 (umask must not leak into a generated script)", got)
	}
}

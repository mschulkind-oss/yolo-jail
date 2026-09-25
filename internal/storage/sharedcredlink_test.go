package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE SHARED CREDENTIAL FILE IS IN JAIL-WRITABLE STATE: every claude jail binds
// <state>/home/.claude-shared-credentials read-write, so the jail can leave a link at
// .credentials.json, or (were the dir not a mountpoint) at the dir itself. EnsureGlobalStorage
// runs as the host user before any config loads, on every launch and `yolo check`, so a
// followed link created, or filled with the legacy credential, a host file of the jail's
// choosing (docs/reference/jail-home.md, "Host code in jail-writable state").
func TestEnsureGlobalStorageNeverFollowsASharedCredentialLink(t *testing.T) {
	setup := func(t *testing.T, legacy bool) (home, sharedDir string) {
		home = t.TempDir()
		t.Setenv("HOME", home)
		sharedDir = filepath.Join(paths.GlobalHome(), sharedCredentialsDir)
		if err := os.MkdirAll(sharedDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if legacy {
			old := filepath.Join(paths.GlobalHome(), ".claude")
			if err := os.MkdirAll(old, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, sharedCredentialsFile), []byte("LEGACY-TOKEN"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return home, sharedDir
	}
	assertRegularHolding := func(t *testing.T, p, want string) {
		t.Helper()
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !fi.Mode().IsRegular() {
			t.Fatalf("%s is %v after EnsureGlobalStorage, want a regular file", p, fi.Mode())
		}
		if got, _ := os.ReadFile(p); string(got) != want {
			t.Errorf("%s holds %q, want %q", p, got, want)
		}
	}

	t.Run("a dangling link at the credential file", func(t *testing.T) {
		home, sharedDir := setup(t, false)
		victim := filepath.Join(home, "victim-created")
		cred := filepath.Join(sharedDir, sharedCredentialsFile)
		if err := os.Symlink(victim, cred); err != nil {
			t.Fatal(err)
		}

		if err := EnsureGlobalStorage(nil); err != nil {
			t.Fatal(err)
		}

		if _, err := os.Lstat(victim); err == nil {
			t.Errorf("EnsureGlobalStorage created %s through the jail's dangling link", victim)
		}
		assertRegularHolding(t, cred, "")
	})

	t.Run("a link to an empty host file, with a legacy credential to migrate", func(t *testing.T) {
		home, sharedDir := setup(t, true)
		victim := filepath.Join(home, "victim-empty")
		if err := os.WriteFile(victim, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		cred := filepath.Join(sharedDir, sharedCredentialsFile)
		if err := os.Symlink(victim, cred); err != nil {
			t.Fatal(err)
		}

		if err := EnsureGlobalStorage(nil); err != nil {
			t.Fatal(err)
		}

		if got, _ := os.ReadFile(victim); len(got) != 0 {
			t.Errorf("the legacy credential was copied through the jail's link into %s: %q", victim, got)
		}
		assertRegularHolding(t, cred, "LEGACY-TOKEN")
	})

	t.Run("a link at the shared dir itself", func(t *testing.T) {
		home, sharedDir := setup(t, true)
		victimDir := filepath.Join(home, "victim-dir")
		if err := os.MkdirAll(victimDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(sharedDir); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victimDir, sharedDir); err != nil {
			t.Fatal(err)
		}

		if err := EnsureGlobalStorage(nil); err != nil {
			t.Fatal(err)
		}

		if ents, _ := os.ReadDir(victimDir); len(ents) != 0 {
			t.Errorf("EnsureGlobalStorage wrote %v into %s through the linked shared dir", ents, victimDir)
		}
	})

	t.Run("the ordinary migration still happens", func(t *testing.T) {
		_, sharedDir := setup(t, true)

		if err := EnsureGlobalStorage(nil); err != nil {
			t.Fatal(err)
		}

		assertRegularHolding(t, filepath.Join(sharedDir, sharedCredentialsFile), "LEGACY-TOKEN")
		if _, err := os.Lstat(filepath.Join(paths.GlobalHome(), ".claude", sharedCredentialsFile)); err == nil {
			t.Error("the legacy credential was not removed after its migration")
		}
	})
}

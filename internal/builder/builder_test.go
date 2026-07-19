package builder

import (
	"testing"
)

func TestSSHConfigBlock(t *testing.T) {
	want := "Host linux-builder\n  Hostname localhost\n  HostKeyAlias linux-builder\n" +
		"  Port 31022\n  User builder\n  IdentityFile /etc/nix/builder_ed25519\n"
	if got := SSHConfigBlock(); got != want {
		t.Errorf("SSHConfigBlock =\n%q\nwant\n%q", got, want)
	}
}

func TestSSHConfigPath(t *testing.T) {
	if got := SSHConfigPath(); got != "/etc/ssh/ssh_config.d/100-linux-builder.conf" {
		t.Errorf("SSHConfigPath = %q", got)
	}
}

func TestNixBuildersLine(t *testing.T) {
	want := "builders = ssh-ng://builder@linux-builder aarch64-linux /etc/nix/builder_ed25519 4 - - - -\n" +
		"builders-use-substitutes = true\n"
	if got := NixBuildersLine(4); got != want {
		t.Errorf("NixBuildersLine(4) =\n%q\nwant\n%q", got, want)
	}
}

func TestTrustedUsersLine(t *testing.T) {
	if got, ok := TrustedUsersLine([]string{"root", "alice"}, "bob"); !ok || got != "trusted-users = root alice bob" {
		t.Errorf("merge = %q, %v", got, ok)
	}
	if _, ok := TrustedUsersLine([]string{"root", "bob"}, "bob"); ok {
		t.Error("already-present me should return ok=false")
	}
	if _, ok := TrustedUsersLine([]string{"root", "@admin"}, "bob"); ok {
		t.Error("@admin coverage should return ok=false")
	}
	if _, ok := TrustedUsersLine([]string{"@wheel"}, "bob"); ok {
		t.Error("@wheel coverage should return ok=false")
	}
	// Dedup keeps order + root first.
	if got, _ := TrustedUsersLine([]string{"alice", "root"}, "bob"); got != "trusted-users = root alice bob" {
		t.Errorf("dedup/order = %q", got)
	}
}

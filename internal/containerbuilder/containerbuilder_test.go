package containerbuilder

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func TestPullArgv(t *testing.T) {
	if got := PullArgv("podman", ""); !reflect.DeepEqual(got, []string{"podman", "pull", BuilderImage}) {
		t.Errorf("podman pull = %v", got)
	}
	if got := PullArgv("container", ""); !reflect.DeepEqual(got, []string{"container", "image", "pull", BuilderImage}) {
		t.Errorf("container pull = %v", got)
	}
}

func TestRunArgv(t *testing.T) {
	// podman publishes the loopback port.
	got := RunArgv("podman", "ssh-ed25519 AAAA", "", "", 0)
	want := []string{
		"podman", "run", "-d", "--rm", "--name", "yolo-linux-builder",
		"-e", "YOLO_BUILDER_PUBKEY=ssh-ed25519 AAAA",
		"-p", "127.0.0.1:31022:22", BuilderImage,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("podman run:\n got %v\n want %v", got, want)
	}
	// Apple Container omits -p.
	got = RunArgv("container", "PUB", "", "", 0)
	want = []string{
		"container", "run", "-d", "--rm", "--name", "yolo-linux-builder",
		"-e", "YOLO_BUILDER_PUBKEY=PUB", BuilderImage,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("container run:\n got %v\n want %v", got, want)
	}
}

func TestNestedNetworkFromPodmanInfo(t *testing.T) {
	for _, tc := range []struct {
		name string
		info string
		want string
		fail bool
	}{
		{
			name: "Podman 6 pasta helper ignores stale rootless default",
			info: `{"version":{"Version":"6.1.4"},"host":{"rootlessNetworkCmd":"slirp4netns","pasta":{"executable":"/nix/store/podman/libexec/podman/pasta"}}}`,
			want: "pasta",
		},
		{
			name: "older Podman with reported slirp helper",
			info: `{"version":{"Version":"5.8.9"},"host":{"slirp4netns":{"executable":"/usr/bin/slirp4netns"}}}`,
			want: "slirp4netns",
		},
		{
			name: "Podman 6 does not fall back to slirp",
			info: `{"version":{"Version":"6.1.4"},"host":{"slirp4netns":{"executable":"/usr/bin/slirp4netns"}}}`,
			fail: true,
		},
		{
			name: "relative helper is not runtime evidence",
			info: `{"version":{"Version":"6.1.4"},"host":{"pasta":{"executable":"pasta"}}}`,
			fail: true,
		},
		{
			name: "malformed extra version component",
			info: `{"version":{"Version":"6.1.4.0"},"host":{"pasta":{"executable":"/bin/pasta"}}}`,
			fail: true,
		},
		{
			name: "legitimate version suffix",
			info: `{"version":{"Version":"6.1.4-1"},"host":{"pasta":{"executable":"/bin/pasta"}}}`,
			want: "pasta",
		},
		{
			name: "legitimate dotted prerelease suffix",
			info: `{"version":{"Version":"6.1.4-rc.1"},"host":{"pasta":{"executable":"/bin/pasta"}}}`,
			want: "pasta",
		},
		{
			name: "malformed version",
			info: `{"version":{"Version":"unknown"},"host":{"pasta":{"executable":"/bin/pasta"}}}`,
			fail: true,
		},
		{
			name: "malformed JSON",
			info: `{`,
			fail: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nestedNetworkFromPodmanInfo(tc.info)
			if tc.fail {
				if err == nil {
					t.Fatalf("network = %q, want failure", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("network = %q, error %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestBuilderURIAndLine(t *testing.T) {
	uri := BuilderURI("127.0.0.1", 0, "/keys/id_ed25519")
	if uri != "ssh-ng://root@127.0.0.1:31022?ssh-key=/keys/id_ed25519" {
		t.Errorf("uri = %q", uri)
	}
	// The SYSTEM field is derived from this host's arch, not frozen: a Linux container on
	// an x86_64 Mac serves x86_64-linux, and advertising aarch64-linux there made nix
	// decline to offload while the builder looked healthy (BACKLOG E8). The rest of the
	// line stays a byte-exact contract, so it is asserted around the one variable field.
	line := BuildersLine("192.168.64.2", 22, 4, "/keys/id_ed25519", "")
	want := "ssh-ng://root@192.168.64.2:22 " + BuilderSystem() + " /keys/id_ed25519 4"
	if line != want {
		t.Errorf("builders line = %q, want %q", line, want)
	}
	// …and BuilderSystem must actually be a linux double, or the assertion above is
	// self-fulfilling: it would pass for any string the function happened to return.
	if !strings.HasSuffix(BuilderSystem(), "-linux") {
		t.Errorf("BuilderSystem() = %q, want an <arch>-linux double — the builder runs a "+
			"LINUX container regardless of the host OS", BuilderSystem())
	}
}

// The GOARCH→nix-system mapping, tested without a cross-compile. amd64 and arm64 are the
// two that ship; the fallthrough is asserted to name what it saw rather than silently
// substituting a default, because a wrong-but-plausible system is exactly how E8 hid.
func TestNixLinuxSystem(t *testing.T) {
	for goarch, want := range map[string]string{
		"amd64":     "x86_64-linux",
		"arm64":     "aarch64-linux",
		"riscv64":   "riscv64-linux",
		"someth1ng": "someth1ng-linux",
	} {
		if got := nixLinuxSystem(goarch); got != want {
			t.Errorf("nixLinuxSystem(%q) = %q, want %q", goarch, got, want)
		}
	}
}

// The PINNED form of the line, which is the only form that authenticates against a
// daemon nix (BuildersLine says why). Byte-exact because nix parses `builders` by
// WHITESPACE POSITION: the host key is field 8, so fields 5-7 have to be spelled even
// though they are defaults, and getting the count wrong feeds the key to
// mandatoryFeatures — where nix would silently require a feature named after a base64
// blob and never offload at all.
func TestBuildersLinePinsTheHostKeyInFieldEight(t *testing.T) {
	const key = "c3NoLWVkMjU1MTkgQUFBQQ=="
	line := BuildersLine("127.0.0.1", 31022, 4, "/keys/id_ed25519", key)
	want := "ssh-ng://root@127.0.0.1:31022 " + BuilderSystem() + " /keys/id_ed25519 4 1 - - " + key
	if line != want {
		t.Errorf("pinned builders line =\n %q\nwant\n %q", line, want)
	}
	if got := len(strings.Fields(line)); got != 8 {
		t.Errorf("a pinned builders line has 8 whitespace-separated fields, got %d: %q", got, line)
	}
}

func TestHostKeyScanArgv(t *testing.T) {
	want := []string{"ssh-keyscan", "-T", "5", "-t", "ed25519", "-p", "31022", "127.0.0.1"}
	if got := HostKeyScanArgv("127.0.0.1", 0); !reflect.DeepEqual(got, want) {
		t.Errorf("HostKeyScanArgv default port = %v, want %v", got, want)
	}
	// Apple Container reaches the VM IP on the guest port, with no publish.
	got := HostKeyScanArgv("192.168.64.2", BuilderGuestPort)
	if !reflect.DeepEqual(got[len(got)-3:], []string{"-p", "22", "192.168.64.2"}) {
		t.Errorf("HostKeyScanArgv AC = %v", got)
	}
}

// EncodeHostKey feeds field 8, and nix base64-DECODES it and writes the bytes straight
// into a known-hosts file after the hostname. So the encoded payload must be exactly
// "<type> <key>" — a comment, a banner line, or an empty scan must not become one.
func TestEncodeHostKey(t *testing.T) {
	const keyType = "ssh-ed25519"
	const keyBody = "AAAAC3NzaC1lZDI1NTE5AAAAIMMliNkaI/Rxhwgydhf+nbofjaDXF95s2hLG15wEktA3"
	// Real `ssh-keyscan -p 31022 -t ed25519 127.0.0.1` output, banner comment included.
	scan := "# 127.0.0.1:31022 SSH-2.0-OpenSSH_10.4\n" +
		"[127.0.0.1]:31022 " + keyType + " " + keyBody + " root@75d91260e0ce\n"
	got := EncodeHostKey(scan)
	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("EncodeHostKey produced non-base64 %q: %v", got, err)
	}
	if string(decoded) != keyType+" "+keyBody {
		t.Errorf("decoded = %q, want %q", decoded, keyType+" "+keyBody)
	}
	// Nothing answered, or only the banner did: no key, and the caller must be able to
	// tell that apart from a key rather than pin the empty string.
	for name, in := range map[string]string{
		"empty":       "",
		"banner only": "# 127.0.0.1:31022 SSH-2.0-OpenSSH_10.4\n",
		"truncated":   "[127.0.0.1]:31022 ssh-ed25519\n",
	} {
		if got := EncodeHostKey(in); got != "" {
			t.Errorf("EncodeHostKey(%s) = %q, want \"\"", name, got)
		}
	}
}

func TestReachableAddressFromContainerLs(t *testing.T) {
	stdout := "ID                  IMAGE  STATE    ADDR\n" +
		"yolo-linux-builder  img    running  192.168.64.2/24\n" +
		"other               img    running  192.168.64.3/24\n"
	host, port, ok := ReachableAddressFromContainerLs(stdout, "yolo-linux-builder")
	if !ok || host != "192.168.64.2" || port != 22 {
		t.Errorf("= %q,%d,%v", host, port, ok)
	}
	// Not present -> not found.
	if _, _, ok := ReachableAddressFromContainerLs(stdout, "missing"); ok {
		t.Error("missing container should not resolve")
	}
	// Header-only -> not found.
	if _, _, ok := ReachableAddressFromContainerLs("ID IMAGE STATE ADDR", "x"); ok {
		t.Error("header-only should not resolve")
	}
}

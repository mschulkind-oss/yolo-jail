package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPrebuiltNativeConstructorsBindTheirActualTLSFixtureAndCommandEnvironment(t *testing.T) {
	for _, overSource := range []bool{false, true} {
		name := "missing-source"
		if overSource {
			name = "opt-in-over-source"
		}
		t.Run(name, func(t *testing.T) {
			previousSuite, previousBin, previousNative := privateSuite, yoloBin, privateNative
			defer func() { privateSuite, yoloBin, privateNative = previousSuite, previousBin, previousNative }()
			machine, home, registry, root, workspace := resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t)
			if err := os.Chmod(registry, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(path, value string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value), mode); err != nil {
					t.Fatal(err)
				}
			}
			pack, bin, build, script := "packtreepb", "packtreepb-x", "make", "#!/bin/sh\necho prebuilt-ran\n"
			if overSource {
				pack, bin, build, script = "packtreeoptin", "packtreeoptin-x", "exit 7", "#!/bin/sh\necho opted-in-prebuilt-ran\n"
			}
			payload := []byte(script)
			var requests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = w.Write(payload)
			}))
			tcpListener := server.Listener
			server.StartTLS()
			t.Cleanup(server.Close)
			certFile := filepath.Join(machine, "server.pem")
			certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
			write(certFile, certificate, 0644)
			marker := filepath.Join(home, ".private-official-producer")
			write(filepath.Join(machine, privateSuiteMarker), "private-suite-v1\n", 0600)
			write(marker, registry, 0600)
			write(filepath.Join(registry, "home-binding"), home, 0600)
			privateSuite.armed, privateSuite.machine = true, machine
			t.Setenv("HOME", home)
			events := filepath.Join(machine, "native-execution-events")
			yoloBin = filepath.Join(machine, "constructor-tripwire")
			write(yoloBin, "#!/bin/sh\necho executed > '"+events+"'\n", 0755)
			fixture := packPrebuiltNativeFixture(home, root, certFile, server, tcpListener, payload, overSource)
			digest := sha256.Sum256(payload)
			manifest := `{"name":"` + pack + `","contributes":[{"kind":"program","bin":"` + bin + `","via":"pack","from":"tools/x","build":"` + build + `","run":"x","prebuilt":{"linux/` + runtime.GOARCH + `":{"url":"` + server.URL + "/" + bin + `","sha256":"` + hex.EncodeToString(digest[:]) + `"}}}]}`
			userConfig := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
			write(userConfig, fixture.userConfig, 0644)
			write(filepath.Join(root, "pack.json"), manifest, 0644)
			if overSource {
				write(filepath.Join(root, "tools", "x", "VERSION"), "1\n", 0644)
			}
			privateNative = fixture
			certEnv := certFile
			construct := func(execute bool) error {
				if err := privateNativeWorkspaceAdmission(workspace); err != nil {
					return err
				}
				cfg := runConfig{env: []string{"SSL_CERT_FILE=" + certEnv}}
				program, args, err := privateLaunchCommandChecked(cfg, yoloBin, jailRunArgs())
				if err != nil {
					return err
				}
				env := append(envWithout("YOLO_VERSION"), "SSL_CERT_FILE="+certEnv)
				cmd, err := privateNativeCommandChecked(context.Background(), program, args, env, fixture)
				if err == nil && execute {
					return cmd.Run()
				}
				return err
			}
			if err := construct(false); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []string{"marker", "home", "endpoint", "path", "checksum", "certificate", "certificate-env", "payload", "build", "override", "extra-source", "workspace"} {
				t.Run(mutation, func(t *testing.T) {
					defer func() {
						write(marker, registry, 0600)
						write(certFile, certificate, 0644)
						write(filepath.Join(root, "pack.json"), manifest, 0644)
						write(userConfig, fixture.userConfig, 0644)
						copy(payload, script)
						certEnv = certFile
						_ = os.Remove(filepath.Join(root, "unclassified-source"))
						_ = os.Remove(filepath.Join(workspace, "mise.toml"))
					}()
					switch mutation {
					case "marker":
						if err := os.Remove(marker); err != nil {
							t.Fatal(err)
						}
					case "home":
						t.Setenv("HOME", machine)
					case "endpoint":
						write(filepath.Join(root, "pack.json"), strings.Replace(manifest, server.URL, "https://example.invalid", 1), 0644)
					case "path":
						write(filepath.Join(root, "pack.json"), strings.Replace(manifest, server.URL+"/"+bin, server.URL+"/unknown?source=vendor", 1), 0644)
					case "checksum":
						write(filepath.Join(root, "pack.json"), strings.Replace(manifest, hex.EncodeToString(digest[:]), strings.Repeat("0", 64), 1), 0644)
					case "certificate":
						write(certFile, "unclassified certificate", 0644)
					case "certificate-env":
						certEnv = "/unclassified/certificate"
					case "payload":
						payload[0] = '!'
					case "build":
						write(filepath.Join(root, "pack.json"), strings.Replace(manifest, `"build":"`+build+`"`, `"build":"npm install"`, 1), 0644)
					case "override":
						write(userConfig, strings.TrimSuffix(fixture.userConfig, "}")+`,"packages":["unclassified"]}`, 0644)
					case "extra-source":
						write(filepath.Join(root, "unclassified-source"), "npm install\n", 0644)
					case "workspace":
						write(filepath.Join(workspace, "mise.toml"), "[tools]\nnode = \"99\"\n", 0644)
					}
					if err := construct(true); err == nil {
						t.Fatalf("native prebuilt constructor accepted %s", mutation)
					}
					if _, err := os.Stat(events); !os.IsNotExist(err) || requests.Load() != 0 {
						t.Fatal("unclassified graph reached executable or TLS side effects")
					}
				})
			}
			server.Close()
			if err := construct(true); err == nil {
				t.Fatal("closed prebuilt server admitted")
			}
		})
	}
}

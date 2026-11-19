package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
)

func packPrebuiltNativeFixture(home, root, certFile string, server *httptest.Server, tcpListener net.Listener, payload []byte, overSource bool) *privateNativeFixture {
	pack, bin, build, script := "packtreepb", "packtreepb-x", "make", "#!/bin/sh\necho prebuilt-ran\n"
	if overSource {
		pack, bin, build, script = "packtreeoptin", "packtreeoptin-x", "exit 7", "#!/bin/sh\necho opted-in-prebuilt-ran\n"
	}
	digest := sha256.Sum256([]byte(script))
	endpoint := server.URL + "/" + bin
	manifest := `{"name":"` + pack + `","contributes":[{"kind":"program","bin":"` + bin + `","via":"pack","from":"tools/x","build":"` + build + `","run":"x","prebuilt":{"linux/` + runtime.GOARCH + `":{"url":"` + endpoint + `","sha256":"` + hex.EncodeToString(digest[:]) + `"}}}]}`
	cfg := `{"packs": [{"source": "file://` + root + `", "name": "` + pack + `"}]}`
	if overSource {
		cfg = `{"packs": [{"source": "file://` + root + `", "name": "` + pack + `"}], "programs": {"overrides": {"` + bin + `": {"prebuilt": true}}}}`
	}
	return &privateNativeFixture{home: home, userConfig: cfg, packs: map[string]string{pack: root}, prebuiltCertFile: certFile, check: func() error {
		if string(payload) != script {
			return fmt.Errorf("private native prebuilt payload drift")
		}
		address, err := url.Parse(endpoint)
		if err != nil || address.Scheme != "https" || address.User != nil || address.Path != "/"+bin || address.RawQuery != "" || address.Fragment != "" || address.RawPath != "" || !net.ParseIP(address.Hostname()).IsLoopback() {
			return fmt.Errorf("private native prebuilt endpoint is not its exact loopback TLS fixture")
		}
		listener, ok := tcpListener.(*net.TCPListener)
		if !ok || server.Listener == nil || listener.Addr().String() != address.Host || server.Listener.Addr().String() != address.Host {
			return fmt.Errorf("private native prebuilt endpoint no longer belongs to its server")
		}
		connection, err := listener.SyscallConn()
		if err != nil {
			return fmt.Errorf("private native prebuilt listener unavailable: %w", err)
		}
		if err := connection.Control(func(uintptr) {}); err != nil {
			return fmt.Errorf("private native prebuilt listener closed: %w", err)
		}
		if err := privateNativeOwnedPath(certFile, false); err != nil {
			return err
		}
		certificate := server.Certificate()
		if certificate == nil {
			return fmt.Errorf("private native prebuilt server has no certificate")
		}
		current, err := os.ReadFile(certFile)
		expected := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
		if err != nil || string(current) != string(expected) {
			return fmt.Errorf("private native prebuilt certificate drift")
		}
		if err := privateNativeJSON(filepath.Join(root, "pack.json"), manifest); err != nil {
			return err
		}
		if !overSource {
			return privateNativeFiles(root, map[string]string{"pack.json": manifest})
		}
		if err := privateNativeDirectories(map[string][]string{root: {"pack.json", "tools"}, filepath.Join(root, "tools"): {"x"}}); err != nil {
			return err
		}
		return privateNativeFiles(filepath.Join(root, "tools", "x"), map[string]string{"VERSION": "1\n"})
	}}
}

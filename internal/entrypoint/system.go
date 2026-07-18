package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
)

// GenerateCABundle mirrors system.generate_ca_bundle's FILE generation: combine
// the image baseline ($SSL_CERT_FILE, unless it already IS our bundle) and each
// path in $NODE_EXTRA_CA_CERTS (colon-separated, de-duplicated) into
// $HOME/.yolo-ca-bundle.crt (chmod 0o644). Always writes a file, even if empty.
//
// The Python function also sets SSL_CERT_FILE / REQUESTS_CA_BUNDLE /
// CURL_CA_BUNDLE / GIT_SSL_CAINFO in os.environ so children inherit the combined
// store; that env mutation is a boot-orchestration concern (Stage 10). This
// generator returns the bundle path so the caller can set those vars; the FILE
// content is what the golden pins. Returns the bundle path.
func GenerateCABundle(e *Env) (string, error) {
	bundlePath := filepath.Join(e.Home, ".yolo-ca-bundle.crt")

	var chunks [][]byte
	baseline := e.Getenv("SSL_CERT_FILE")
	if baseline != "" && baseline != bundlePath {
		if data := readBundleBytes(baseline); len(data) > 0 {
			chunks = append(chunks, data)
		}
	}

	if extras := e.Getenv("NODE_EXTRA_CA_CERTS"); extras != "" {
		seen := map[string]struct{}{}
		for _, raw := range splitPathList(extras) {
			p := trimSpace(raw)
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			if data := readBundleBytes(p); len(data) > 0 {
				chunks = append(chunks, data)
			}
		}
	}

	// body = b"\n".join(c.rstrip(b"\n") for c in chunks); ensure trailing NL if
	// non-empty and not already ending in one.
	trimmed := make([][]byte, len(chunks))
	for i, c := range chunks {
		trimmed[i] = bytes.TrimRight(c, "\n")
	}
	body := bytes.Join(trimmed, []byte("\n"))
	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		body = append(body, '\n')
	}
	if err := os.MkdirAll(e.Home, 0o755); err != nil {
		return "", err
	}
	// write_bytes then chmod 0o644 (WriteInPlace truncates in place).
	if err := writeBytesMode(bundlePath, body, 0o644); err != nil {
		return "", err
	}
	return bundlePath, nil
}

// readBundleBytes reads a PEM file, returning nil on any error (best-effort;
// mirrors system._read_bundle_bytes returning b"").
func readBundleBytes(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// splitPathList splits on os.pathsep (":" on Linux). Python uses os.pathsep;
// the entrypoint always runs on Linux, so ":" is correct. Kept explicit.
func splitPathList(s string) []string {
	return splitByte(s, ':')
}

func splitByte(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// trimSpace trims ASCII whitespace like Python's str.strip() (for the common
// PEM-path case; the paths never contain exotic unicode whitespace).
func trimSpace(s string) string {
	start := 0
	for start < len(s) && isSpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

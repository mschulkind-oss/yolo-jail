package callbrokerinput

import (
	"errors"
	"strings"
)

// HTTPClientCell is one client/setup publication cell of a selected private HTTP client
// (pack-brokers-http-plan.md, Task 4): the client's pack, bin and selected recipe identity, and
// the setup its launch froze in the prepared record. A cell publishes the front's URL, local token
// and trust only once its own acquisition/replay, managed-startup, local TLS/client and native
// isolation evidence and independent review have passed.
type HTTPClientCell struct {
	Pack, Bin, RecipeIdentity, Setup string
}

// publishedHTTPClientCells is data, not a branch: the host's publication check and the in-jail
// bootstrap's prefilter read it the same way for any entry. It is empty, so publication is off for
// every cell: no client/setup cell has its per-cell evidence yet (the plan's Task 4 status names
// what each one owes). The setup is the prepared record's frozen setup name, which does not tell a
// rootful from a rootless Podman, so a podman-self cell is listed only once both are proven.
var publishedHTTPClientCells = []HTTPClientCell{}

// HTTPClientCellPublished reports whether cell may receive its front's URL, token and trust.
func HTTPClientCellPublished(cell HTTPClientCell) bool {
	for _, c := range publishedHTTPClientCells {
		if c == cell {
			return true
		}
	}
	return false
}

// HTTPClientBinMayBePublished reports whether any published cell names bin. The in-jail bootstrap
// uses it only to skip asking the host for a bin no cell names; the host decides the whole cell.
func HTTPClientBinMayBePublished(bin string) bool {
	for _, c := range publishedHTTPClientCells {
		if c.Bin == bin {
			return true
		}
	}
	return false
}

// HTTPClientPublishOp is the host operation through which a selected private client's bootstrap
// asks for its front's publication.
const HTTPClientPublishOp = "client"

// HTTPClientPublication is what the host publishes to one selected client of a sealed, live
// front: the local token (the whole value of the client's ordinary token variable), the front's
// HTTPS URL, the front's pinned certificate as the client's only trust, and the three ordinary
// variable names the selected pack declared for them. It crosses only the jail's own
// authenticated host endpoint and the client process's environment.
type HTTPClientPublication struct {
	Token             string `json:"token"`
	URL               string `json:"url"`
	CertificateBase64 string `json:"certificate_base64"`
	TokenVariable     string `json:"token_variable"`
	URLVariable       string `json:"url_variable"`
	TrustVariable     string `json:"trust_variable"`
}

// Validate checks a publication's shape: three distinct, well-formed variable names, an https
// loopback URL, a token and a certificate. It does not verify the certificate against the URL;
// the host pinned that at seal time.
func (p HTTPClientPublication) Validate() error {
	names := map[string]bool{}
	for _, name := range []string{p.TokenVariable, p.URLVariable, p.TrustVariable} {
		if !validEnvName(name) || names[name] {
			return errors.New("HTTP client publication names an invalid or repeated variable")
		}
		names[name] = true
	}
	address, ok := strings.CutPrefix(p.URL, "https://")
	if !ok {
		return errors.New("HTTP client publication URL is not https")
	}
	if _, ok := ValidHTTPFrontAddress(address); !ok {
		return errors.New("HTTP client publication URL is not the front's loopback address")
	}
	if p.Token == "" || strings.ContainsAny(p.Token, "\x00\r\n") || p.CertificateBase64 == "" {
		return errors.New("HTTP client publication has no usable token or certificate")
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		return false
	}
	for _, c := range name {
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

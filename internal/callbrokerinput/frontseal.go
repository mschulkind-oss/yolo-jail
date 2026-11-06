package callbrokerinput

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

const PreparedHTTPFrontSealVersion = "yolo.http-front-seal/1"

// PreparedHTTPFrontSeal is the immutable host record a selected HTTP front writes
// beside the prepared generation once it has bound and minted its certificate. It
// binds the generation to the live broker start, the reported loopback address and
// the pinned front certificate. It never holds the local token or its hash.
type PreparedHTTPFrontSeal struct {
	Version           string `json:"version"`
	Generation        string `json:"generation"`
	Container         string `json:"container"`
	FrontJailID       string `json:"front_jail_id"`
	LaunchID          string `json:"launch_id"`
	BrokerStart       string `json:"broker_start"`
	Loophole          string `json:"loophole"`
	DeclarationHash   string `json:"declaration_hash"`
	Address           string `json:"address"`
	CertificateSHA256 string `json:"certificate_sha256"`
	CertificateDER    []byte `json:"certificate_der"`
}

var frontSealFields = map[string]bool{"version": true, "generation": true, "container": true,
	"front_jail_id": true, "launch_id": true, "broker_start": true, "loophole": true,
	"declaration_hash": true, "address": true, "certificate_sha256": true, "certificate_der": true}

// ValidHTTPFrontGeneration reports whether s is a 32-character lowercase hex generation.
func ValidHTTPFrontGeneration(s string) bool {
	if len(s) != 32 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// ValidHTTPFrontAddress reports whether address is a literal loopback IP with a nonzero port.
func ValidHTTPFrontAddress(address string) (netip.AddrPort, bool) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return netip.AddrPort{}, false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || !ip.IsLoopback() {
		return netip.AddrPort{}, false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != port {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ip, uint16(n)), true
}

// Validate checks the exact v1 seal schema and that the pinned certificate
// parses, names the sealed address and allows server authentication, so a reader
// that trusts DecodePreparedHTTPFrontSeal alone gets a self-consistent seal.
func (s PreparedHTTPFrontSeal) Validate() error {
	if s.Version != PreparedHTTPFrontSealVersion {
		return errors.New("HTTP front seal version is invalid")
	}
	if !ValidHTTPFrontGeneration(s.Generation) {
		return errors.New("HTTP front seal generation is invalid")
	}
	for name, value := range map[string]string{
		"container": s.Container, "front jail id": s.FrontJailID, "launch id": s.LaunchID,
		"broker start": s.BrokerStart, "loophole": s.Loophole, "declaration hash": s.DeclarationHash,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("HTTP front seal is missing %s", name)
		}
	}
	if _, ok := ValidHTTPFrontAddress(s.Address); !ok {
		return errors.New("HTTP front seal address is not a loopback address with a port")
	}
	sum := sha256.Sum256(s.CertificateDER)
	if len(s.CertificateDER) == 0 || s.CertificateSHA256 != hex.EncodeToString(sum[:]) {
		return errors.New("HTTP front seal certificate digest does not match its certificate")
	}
	return CheckHTTPFrontCertificate(s.CertificateDER, s.Address)
}

// CheckHTTPFrontCertificate requires der to parse as a certificate that names
// address's loopback IP as an IP SAN and allows server authentication. The host
// runs it before writing a seal, and Validate runs it again for every reader.
func CheckHTTPFrontCertificate(der []byte, address string) error {
	addr, ok := ValidHTTPFrontAddress(address)
	if !ok {
		return errors.New("HTTP front address is not a literal loopback IP with a nonzero port")
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return errors.New("HTTP front certificate does not parse")
	}
	ip := net.IP(addr.Addr().AsSlice())
	named := false
	for _, san := range certificate.IPAddresses {
		if san.Equal(ip) {
			named = true
		}
	}
	if !named {
		return errors.New("HTTP front certificate does not name its loopback address")
	}
	for _, usage := range certificate.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth {
			return nil
		}
	}
	return errors.New("HTTP front certificate is not for server authentication")
}

// DecodePreparedHTTPFrontSeal decodes a seal record strictly: one flat object of
// exact, unduplicated string fields, no trailing JSON, then Validate.
func DecodePreparedHTTPFrontSeal(data []byte) (PreparedHTTPFrontSeal, error) {
	var seal PreparedHTTPFrontSeal
	if err := rejectFrontSealShape(data); err != nil {
		return seal, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&seal); err != nil {
		return seal, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return seal, errors.New("HTTP front seal has trailing JSON")
	}
	return seal, seal.Validate()
}

// rejectFrontSealShape refuses unknown, aliased (case-folded) and duplicate keys
// and any non-string value before encoding/json's case-insensitive matching runs.
func rejectFrontSealShape(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return errors.New("HTTP front seal is not a JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, _ := keyToken.(string)
		if !frontSealFields[key] {
			return fmt.Errorf("HTTP front seal has unknown or aliased field %q", key)
		}
		if seen[key] {
			return fmt.Errorf("HTTP front seal has duplicate field %q", key)
		}
		seen[key] = true
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("HTTP front seal field %q is not a string", key)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("HTTP front seal has trailing JSON")
	}
	return nil
}

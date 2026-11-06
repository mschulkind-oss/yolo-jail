package callbroker

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const httpFrontSealOp = "seal"

// Refusals for the selected HTTP front's generation gate and seal. Each names the
// next step; the jail cannot repair a stale or foreign generation itself.
var (
	errFrontNotSealed        = errors.New("the selected HTTP front has not sealed this generation; stop the jail and relaunch")
	errFrontGenerationWrong  = errors.New("HTTP front generation does not match this launch; stop the jail and relaunch")
	errFrontAlreadySealed    = errors.New("HTTP front seal already exists for this generation; stop the jail and relaunch")
	errFrontTokenOutsideSlot = errors.New("the request carries the local front token outside its field; remove it and retry")
)

// httpFrontState is the broker's in-memory view of its one sealed front. It is
// never consulted by currentAuthority or revalidateHTTP: yolo approve builds a
// fresh handler with no front state and must keep working.
type httpFrontState struct {
	mu    sync.Mutex
	seal  *callbrokerinput.PreparedHTTPFrontSeal
	token string
}

type httpFrontSealRequest struct {
	Address           string `json:"address"`
	CertificateBase64 string `json:"certificate_base64"`
}

type httpFrontSealReply struct {
	TokenSHA256      string `json:"token_sha256"`
	CredentialHeader string `json:"credential_header"`
	CredentialPrefix string `json:"credential_prefix"`
}

// checkFrontGeneration is the request-frame gate that runs before serveHTTP. A
// selected input with a prepared generation accepts only frames naming it, only
// after this broker start sealed it, and never a frame carrying the sealed token.
// A legacy input accepts no generation at all.
func (h *HTTPHandler) checkFrontGeneration(present bool, generation string, frame []byte, body []byte) error {
	want := h.input.HTTPFrontGeneration
	if want == "" {
		if present {
			return errFrontGenerationWrong
		}
		return nil
	}
	if !present || generation != want {
		return errFrontGenerationWrong
	}
	h.front.mu.Lock()
	defer h.front.mu.Unlock()
	if h.front.seal == nil || h.start == "" || h.front.seal.BrokerStart != h.start || h.front.seal.Generation != want || h.front.token == "" {
		return errFrontNotSealed
	}
	token := []byte(h.front.token)
	if bytes.Contains(frame, token) || bytes.Contains(body, token) {
		return errFrontTokenOutsideSlot
	}
	return nil
}

// seal records the selected front's loopback address and pinned certificate for
// this broker start, mints the local token into a host-private leaf, and returns
// only the token's hash and the binding's credential field to the front.
func (h *HTTPHandler) seal(jail, generation string, req httpFrontSealRequest) (httpFrontSealReply, error) {
	var reply httpFrontSealReply
	if h.input.HTTPFrontGeneration == "" {
		return reply, errors.New("this launch has no selected HTTP front generation; relaunch with the client pack selected")
	}
	if generation != h.input.HTTPFrontGeneration {
		return reply, errFrontGenerationWrong
	}
	if jail == "" || jail != h.input.FrontJailID {
		return reply, errors.New("HTTP front seal did not come from this launch's jail; stop the jail and relaunch")
	}
	if h.start == "" {
		return reply, errors.New("call broker has no bound host launch; stop the jail and relaunch")
	}
	if err := validatePreparedHTTPGeneration(h.inputFile, h.input); err != nil {
		return reply, err
	}
	sf, _, err := h.currentAuthority()
	if err != nil {
		return reply, err
	}
	h.front.mu.Lock()
	defer h.front.mu.Unlock()
	sealPath, tokenPath := h.inputFile+".prepared.seal", h.inputFile+".prepared.token"
	if h.front.seal != nil {
		return reply, errFrontAlreadySealed
	}
	for _, leaf := range []string{sealPath, tokenPath} {
		if _, err := os.Lstat(leaf); !errors.Is(err, os.ErrNotExist) {
			return reply, errFrontAlreadySealed
		}
	}
	if _, ok := callbrokerinput.ValidHTTPFrontAddress(req.Address); !ok {
		return reply, errors.New("HTTP front address must be a literal loopback IP with a nonzero port; stop the jail and relaunch")
	}
	der, err := base64.StdEncoding.Strict().DecodeString(req.CertificateBase64)
	if err != nil || len(der) == 0 {
		return reply, errors.New("HTTP front certificate is not valid base64; stop the jail and relaunch")
	}
	if err := callbrokerinput.CheckHTTPFrontCertificate(der, req.Address); err != nil {
		return reply, errors.New(err.Error() + "; stop the jail and relaunch")
	}
	sum := sha256.Sum256(der)
	record := callbrokerinput.PreparedHTTPFrontSeal{Version: callbrokerinput.PreparedHTTPFrontSealVersion,
		Generation: generation, Container: sf.Container, FrontJailID: h.input.FrontJailID, LaunchID: sf.LaunchID,
		BrokerStart: h.start, Loophole: h.input.Loophole, DeclarationHash: h.input.DeclarationHash,
		Address: req.Address, CertificateSHA256: hex.EncodeToString(sum[:]), CertificateDER: der}
	if err := record.Validate(); err != nil {
		return reply, errors.New("HTTP front seal is incomplete: " + err.Error() + "; stop the jail and relaunch")
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return reply, err
	}
	token, err := svcendpoint.NewToken()
	if err != nil {
		return reply, errors.New("HTTP front token could not be minted; stop the jail and relaunch")
	}
	if err := writeExclusivePrivateLeaf(tokenPath, []byte(token)); err != nil {
		return reply, errors.New("HTTP front token could not be recorded; stop the jail and relaunch")
	}
	if h.beforeSealWrite != nil {
		h.beforeSealWrite()
	}
	if err := writeExclusivePrivateLeaf(sealPath, encoded); err != nil {
		_ = os.Remove(tokenPath)
		return reply, errors.New("HTTP front seal could not be recorded; stop the jail and relaunch")
	}
	h.front.seal, h.front.token = &record, token
	tokenSum := sha256.Sum256([]byte(token))
	credential := h.input.HTTPBinding.HTTP.Credential
	return httpFrontSealReply{TokenSHA256: hex.EncodeToString(tokenSum[:]),
		CredentialHeader: credential.Header, CredentialPrefix: credential.Prefix}, nil
}

// handleSeal answers the selected front's one side-effect-free seal operation.
// Its jail identity is the authenticated preamble's, never the frame's hint.
func (h *HTTPHandler) handleSeal(s *hostservice.Session, generation string) {
	raw, present := s.Get("front")
	encoded, err := jsonx.DumpsCompact(raw)
	var req httpFrontSealRequest
	if !present || err != nil || json.Unmarshal([]byte(encoded), &req) != nil {
		s.Stderr("Invalid HTTP front seal; update the selected front.\n")
		s.Exit(64)
		return
	}
	reply, err := h.seal(s.JailID, generation, req)
	if err != nil {
		s.Stderr("HTTP front seal refused: " + err.Error() + "\n")
		s.Exit(64)
		return
	}
	answer, err := json.Marshal(reply)
	if err != nil {
		s.Stderr("HTTP front seal reply cannot be encoded; stop the jail and relaunch.\n")
		s.Exit(64)
		return
	}
	s.StdoutBytes(answer)
	s.Exit(0)
}

// writeExclusivePrivateLeaf creates path 0600 and refuses an existing file or link.
func writeExclusivePrivateLeaf(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

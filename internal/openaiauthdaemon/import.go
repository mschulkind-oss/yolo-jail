package openaiauthdaemon

// import.go reads a Codex `auth.json` a human already has and turns it into the
// provider-neutral tokens Broker.Replace installs as the next canonical generation.
//
// It is openauthclient.WriteCodexAuth's MIRROR IMAGE, and the asymmetry is the interesting
// part: that writer renders a broker VIEW (its `refresh_token` is the opaque
// `yolo-broker:<generation>` marker, never a credential), so a file this tree wrote is exactly
// the file this reader must refuse. Importing one would install a marker as the canonical
// refresh token and log the machine out of a grant nothing could recover — which is why the
// marker check below is a refusal and not a convenience.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
)

// maxCodexAuthBytes bounds the read. A credential file is ~2 KB; anything near this is not one,
// and the daemon must not be made to buffer a file a caller points it at.
const maxCodexAuthBytes = 1 << 20

// brokerRefreshMarker matches the opaque generation marker yolo hands agents in place of the
// canonical refresh token. Spelled here as well as in openauthclient because the two ends
// enforce it in OPPOSITE directions — that package refuses to WRITE an agent view whose
// refresh credential is not a marker, this one refuses to READ one that is — and a shared
// helper would make the two look like one rule with one direction.
var brokerRefreshMarker = regexp.MustCompile(`^yolo-broker:`)

type codexAuthFile struct {
	Tokens struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

// ReadCodexAuthFile decodes the named Codex credential file.
//
// WHAT IT REFUSES, and why each one is a refusal rather than a best effort:
//
//   - an empty or relative path — the daemon runs with the host user's whole filesystem in
//     reach and resolves nothing on the caller's behalf;
//   - a SYMLINK, or anything that is not a regular file. The caller names a path and the
//     daemon reads it as the host user, so following a link is the one step that would let a
//     path mean a different file than the one the human typed. Checked with Lstat before the
//     open, and re-checked on the opened file, so the answer cannot change in between;
//   - a file whose `refresh_token` is a `yolo-broker:` marker (see the header);
//   - a missing access, id or refresh token — Broker.Replace requires all three, and a
//     partial import would install a generation nothing can refresh.
//
// WHAT IT DOES NOT REFUSE IS A LAPSED ACCESS TOKEN, deliberately, and the plan that asked for
// "refuses a malformed or expired file" is overtaken on this one clause. An access token's
// lifetime is minutes; a file sitting on disk has almost always outlived it, and the thing
// being imported is the REFRESH token. Refusing would reject the common case while protecting
// nothing: the broker refreshes when due, and a dead refresh token surfaces as
// ErrLoginRequired on first use either way. So an expiry that cannot be read at all becomes
// "due now" rather than an error.
func ReadCodexAuthFile(path string) (openaiauth.Tokens, error) {
	if strings.TrimSpace(path) == "" {
		return openaiauth.Tokens{}, errors.New("OpenAI credential import needs the path of a Codex auth.json")
	}
	if !filepath.IsAbs(path) {
		return openaiauth.Tokens{}, fmt.Errorf("OpenAI credential import path must be absolute: %s", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return openaiauth.Tokens{}, fmt.Errorf("read Codex credential file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return openaiauth.Tokens{}, fmt.Errorf("refusing to import a symlink: %s", path)
	}
	if !info.Mode().IsRegular() {
		return openaiauth.Tokens{}, fmt.Errorf("refusing to import a non-regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return openaiauth.Tokens{}, fmt.Errorf("read Codex credential file: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return openaiauth.Tokens{}, fmt.Errorf("read Codex credential file: %w", err)
	}
	if !opened.Mode().IsRegular() {
		return openaiauth.Tokens{}, fmt.Errorf("refusing to import a non-regular file: %s", path)
	}
	if opened.Size() > maxCodexAuthBytes {
		return openaiauth.Tokens{}, fmt.Errorf("refusing to import %d bytes as a credential file: %s",
			opened.Size(), path)
	}
	data := make([]byte, opened.Size())
	if _, err := file.Read(data); err != nil && opened.Size() > 0 {
		return openaiauth.Tokens{}, fmt.Errorf("read Codex credential file: %w", err)
	}
	return decodeCodexAuth(data, time.Now())
}

// decodeCodexAuth is the pure half, so every refusal above is testable without a file and the
// expiry derivation is testable without a clock.
func decodeCodexAuth(data []byte, now time.Time) (openaiauth.Tokens, error) {
	var file codexAuthFile
	if err := json.Unmarshal(data, &file); err != nil {
		return openaiauth.Tokens{}, fmt.Errorf("decode Codex credential file: %w", err)
	}
	t := file.Tokens
	if brokerRefreshMarker.MatchString(t.RefreshToken) {
		return openaiauth.Tokens{}, errors.New(
			"refusing to import a yolo broker view: its refresh_token is a generation marker, " +
				"not a credential, and installing it would replace the canonical grant with " +
				"something nothing can redeem. Import the auth.json Codex wrote from its own login")
	}
	if t.AccessToken == "" || t.IDToken == "" || t.RefreshToken == "" {
		return openaiauth.Tokens{}, errors.New(
			"codex credential file is missing an access, id or refresh token")
	}
	expires := jwtExpiry(t.AccessToken)
	if expires.IsZero() {
		// DUE NOW, not an error: the refresh token is what is being imported, and the
		// broker's first request refreshes a generation that is already due.
		expires = now
	}
	return openaiauth.Tokens{
		AccessToken:  t.AccessToken,
		IDToken:      t.IDToken,
		RefreshToken: t.RefreshToken,
		ExpiresAt:    expires,
		AccountID:    t.AccountID,
	}, nil
}

// jwtExpiry reads the `exp` claim of a JWT's payload, or the zero time when there is nothing
// to read. Codex's auth.json records no expiry of its own, so the access token is where the
// answer is — the same place Codex itself reads it.
//
// NO SIGNATURE VERIFICATION, and none is wanted: this is not authentication. The file belongs
// to the host user who named it, the value is used only to decide when the broker's first
// refresh is due, and a wrong answer costs one early refresh. Verifying would mean shipping
// OpenAI's signing keys to answer a scheduling question.
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

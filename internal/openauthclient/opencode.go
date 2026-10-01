package openauthclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// opencodeProviderID is the key opencode's Auth store files the OpenAI credential under: its
// built-in `openai` provider, which its built-in ChatGPT support (CodexAuthPlugin) puts on the
// subscription whenever the entry is an `oauth` one (opencode 1.18.34, src/plugin/openai/codex.ts).
const opencodeProviderID = "openai"

// opencodeOAuthCredential is opencode's `oauth` auth entry (src/auth/index.ts, Auth.Oauth):
// `expires` must be a non-negative integer of epoch milliseconds, or opencode's schema drops the
// whole entry on read.
type opencodeOAuthCredential struct {
	Type      string `json:"type"`
	Refresh   string `json:"refresh"`
	Access    string `json:"access"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"accountId,omitempty"`
}

// OpencodeLogin is what an opencode auth store holds under `openai`, the one key opencode files
// its own ChatGPT login, an OpenAI API key and yolo's view under alike.
type OpencodeLogin int

const (
	// OpencodeNoLogin: no store, no `openai` entry, or a store that cannot be read.
	OpencodeNoLogin OpencodeLogin = iota
	// OpencodeSharedLogin: yolo's view, an `oauth` entry whose refresh value is the broker's
	// generation marker.
	OpencodeSharedLogin
	// OpencodeOwnLogin: opencode's own ChatGPT login, an `oauth` entry with a refresh token of its own.
	OpencodeOwnLogin
	// OpencodeAPIKey: an OpenAI API key the user stored in opencode's /connect.
	OpencodeAPIKey
	// OpencodeOtherLogin: any other entry.
	OpencodeOtherLogin
)

// classifyOpencodeLogin reads one `openai` entry of an opencode auth.json.
func classifyOpencodeLogin(raw json.RawMessage) OpencodeLogin {
	if len(raw) == 0 || string(raw) == "null" {
		return OpencodeNoLogin
	}
	var entry struct {
		Type    string `json:"type"`
		Refresh string `json:"refresh"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return OpencodeOtherLogin
	}
	switch {
	case entry.Type == "oauth" && strings.HasPrefix(entry.Refresh, "yolo-broker:"):
		return OpencodeSharedLogin
	case entry.Type == "oauth":
		return OpencodeOwnLogin
	case entry.Type == "api":
		return OpencodeAPIKey
	}
	return OpencodeOtherLogin
}

// ReadOpencodeLogin is what the opencode auth store at path holds under `openai`. It only reads:
// `yolo host` asks it about the user's own store, which yolo never writes.
func ReadOpencodeLogin(path string) OpencodeLogin {
	data, err := os.ReadFile(path)
	if err != nil {
		return OpencodeNoLogin
	}
	entries := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &entries); err != nil {
		return OpencodeOtherLogin
	}
	return classifyOpencodeLogin(entries[opencodeProviderID])
}

// WriteOpencodeAuth merges a broker token view into opencode's auth.json under `openai`, keeping
// every other provider's entry. The refresh value is the broker's generation marker, never a
// credential: yolo's opencode plugin (packs/opencode/plugins/yolo-openai-auth.js) serves every
// request from the broker, and only an entry carrying that marker is one it serves, so opencode
// never refreshes the token itself (docs/design/openai-auth-broker.md OQ-OA2).
//
// It returns what the write replaced under `openai`. That key is also where opencode files its
// own ChatGPT login and an OpenAI API key, so the view REPLACES either one, and the caller says
// so (Run): a launch never discards a login of the user's own in silence.
//
// opencode rewrites the whole file on every change and takes no lock (Auth.set), so there is no
// lock to share as pi's writer shares pi's; the write is one atomic rename of a mode-0600 file,
// the mode opencode itself writes.
func WriteOpencodeAuth(path string, response json.RawMessage) (OpencodeLogin, error) {
	if path == "" {
		return OpencodeNoLogin, errors.New("opencode auth path is required")
	}
	var view tokenView
	if err := json.Unmarshal(response, &view); err != nil {
		return OpencodeNoLogin, fmt.Errorf("decode opencode credential view: %w", err)
	}
	if view.AccessToken == "" || view.ExpiresAtMS <= 0 || view.Generation <= 0 {
		return OpencodeNoLogin, errors.New("OpenAI credential service returned an incomplete opencode view")
	}
	entries := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &entries); err != nil {
			return OpencodeNoLogin, fmt.Errorf("decode existing opencode auth.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return OpencodeNoLogin, fmt.Errorf("read opencode auth.json: %w", err)
	}
	if entries == nil {
		entries = map[string]json.RawMessage{}
	}
	replaced := classifyOpencodeLogin(entries[opencodeProviderID])
	credential, err := json.Marshal(opencodeOAuthCredential{
		Type: "oauth", Refresh: fmt.Sprintf("yolo-broker:%d", view.Generation),
		Access: view.AccessToken, Expires: view.ExpiresAtMS, AccountID: view.AccountID,
	})
	if err != nil {
		return OpencodeNoLogin, fmt.Errorf("encode opencode credential view: %w", err)
	}
	entries[opencodeProviderID] = credential
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return OpencodeNoLogin, fmt.Errorf("encode opencode auth.json: %w", err)
	}
	if err := atomicWritePrivate(path, append(data, '\n')); err != nil {
		return OpencodeNoLogin, err
	}
	return replaced, nil
}

// opencodeReplacedNotice is the line the launch prints when the view replaced a login of the
// user's own, or "" when it replaced none (no entry, or yolo's own earlier view).
func opencodeReplacedNotice(replaced OpencodeLogin, path string) string {
	var what, after string
	switch replaced {
	case OpencodeOwnLogin:
		what = "opencode's own ChatGPT login"
	case OpencodeAPIKey:
		what = "an OpenAI API key"
		after = " To use the key on a launch that is not on the subscription, enter it again in opencode's /connect."
	case OpencodeOtherLogin:
		what = "the OpenAI login opencode stored"
	default:
		return ""
	}
	return fmt.Sprintf("  opencode: yolo replaced %s in %s with the shared ChatGPT login, which opencode "+
		"uses on the ChatGPT subscription.%s", what, path, after)
}

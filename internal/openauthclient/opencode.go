package openauthclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// WriteOpencodeAuth merges a broker token view into opencode's auth.json under `openai`, keeping
// every other provider's entry. The refresh value is the broker's generation marker, never a
// credential: yolo's opencode plugin (packs/opencode/plugins/yolo-openai-auth.js) serves every
// request from the broker, and only an entry carrying that marker is one it serves, so opencode
// never refreshes the token itself (docs/design/openai-auth-broker.md OQ-OA2).
//
// opencode rewrites the whole file on every change and takes no lock (Auth.set), so there is no
// lock to share as pi's writer shares pi's; the write is one atomic rename of a mode-0600 file,
// the mode opencode itself writes.
func WriteOpencodeAuth(path string, response json.RawMessage) error {
	if path == "" {
		return errors.New("opencode auth path is required")
	}
	var view tokenView
	if err := json.Unmarshal(response, &view); err != nil {
		return fmt.Errorf("decode opencode credential view: %w", err)
	}
	if view.AccessToken == "" || view.ExpiresAtMS <= 0 || view.Generation <= 0 {
		return errors.New("OpenAI credential service returned an incomplete opencode view")
	}
	entries := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &entries); err != nil {
			return fmt.Errorf("decode existing opencode auth.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read opencode auth.json: %w", err)
	}
	if entries == nil {
		entries = map[string]json.RawMessage{}
	}
	credential, err := json.Marshal(opencodeOAuthCredential{
		Type: "oauth", Refresh: fmt.Sprintf("yolo-broker:%d", view.Generation),
		Access: view.AccessToken, Expires: view.ExpiresAtMS, AccountID: view.AccountID,
	})
	if err != nil {
		return fmt.Errorf("encode opencode credential view: %w", err)
	}
	entries[opencodeProviderID] = credential
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode opencode auth.json: %w", err)
	}
	return atomicWritePrivate(path, append(data, '\n'))
}

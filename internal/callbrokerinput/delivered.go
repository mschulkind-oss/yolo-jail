package callbrokerinput

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func InputDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// CaptureDelivered is fed only the validated in-memory gate workspace view.
// Missing and null remain distinct via the surrounding object/presence, not an
// effective decision that would discard false/default/scoped asks.
func CaptureDelivered(source, pack, model string, workspace *jsonx.OrderedMap) (brokerscope.DeliveredAsks, error) {
	var asks brokerscope.DeliveredAsks
	if workspace == nil {
		return asks, errors.New("gate workspace asks unavailable")
	}
	requests := jsonx.NewOrderedMap()
	var rawSource, packRead any
	if raw, ok := workspace.Get("requests"); ok {
		if all, ok := raw.(*jsonx.OrderedMap); ok {
			rawSource, _ = all.Get(source)
			if rawPack, ok := all.Get(pack); ok {
				if table, ok := rawPack.(*jsonx.OrderedMap); ok {
					packRead, _ = table.Get("read")
				} else {
					packRead = rawPack
				}
			}
		}
	}
	// Preserve presence independently of the value (null is not absence).
	if raw, ok := workspace.Get("requests"); ok {
		if all, ok := raw.(*jsonx.OrderedMap); ok {
			if _, present := all.Get(source); present {
				requests.Set("source", rawSource)
			}
			if rawPack, present := all.Get(pack); present {
				if table, ok := rawPack.(*jsonx.OrderedMap); ok {
					if read, present := table.Get("read"); present {
						requests.Set("pack_read", read)
					}
				} else {
					requests.Set("pack_read", rawPack)
				}
			}
		}
	}
	selectedScopes := jsonx.NewOrderedMap()
	if raw, ok := workspace.Get("scopes"); ok {
		if scopes, ok := raw.(*jsonx.OrderedMap); ok {
			for _, alias := range scopes.Keys() {
				raw, _ := scopes.Get(alias)
				if scope, ok := raw.(*jsonx.OrderedMap); ok {
					if scopedSource, _ := scope.Get("source"); scopedSource == source || scopedSource == pack {
						selectedScopes.Set(alias, scope)
					}
				}
			}
		}
	}
	encode := func(value any) (json.RawMessage, error) {
		text, err := jsonx.DumpsCompact(value)
		return json.RawMessage(text), err
	}
	var err error
	asks.Requests, err = encode(requests)
	if err != nil {
		return asks, err
	}
	asks.Scopes, err = encode(selectedScopes)
	if err != nil {
		return asks, err
	}
	asks.PackRead, err = encode(packRead)
	if err != nil {
		return asks, err
	}
	asks.Model = model
	return asks, nil
}

// PublishDelivered is the normal protected initial producer, not a retrospective
// approval importer. /2 input bytes are not changed and no consumption is asserted.
func PublishDelivered(scopePath string, data []byte, asks brokerscope.DeliveredAsks) error {
	var input Launch
	if err := json.Unmarshal(data, &input); err != nil || input.Version != "yolo.call-broker-launch/2" || input.ScopeFile != scopePath {
		return errors.New("delivered input binding unavailable")
	}
	return brokerlock.WithTransaction(paths.BrokerSourceDir(input.Source), func() error {
		sf, err := brokerscope.ReadFile(scopePath)
		if err != nil {
			return err
		}
		if sf.CallInput == "" || sf.Source != input.Source {
			return errors.New("delivered scope not bound to selected input")
		}
		_, err = brokerscope.PublishDeliveredLocked(sf, brokerscope.DeliveredBaseline{Binding: brokerscope.PolicyBinding{Source: sf.Source, Workspace: sf.Workspace, LaunchID: sf.LaunchID, DeclarationHash: input.DeclarationHash, InputDigest: InputDigest(data)}, Asks: asks, ReadStanding: input.ReadStanding, ReadGrantStanding: input.ReadGrantStanding, ReadWithheld: input.ReadWithheld})
		return err
	})
}

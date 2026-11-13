package callbrokerinput

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// PreparedHTTPClientEntrypointsVersion is the immutable record that joins a prepared generation
// (PreparedHTTPClientGeneration) to each selected client's verified capture entrypoint
// (docs/design/pack-brokers-http-plan.md, Task 2 first slice). It is written beside the prepared
// record, never into it: Task 1's prepared identity is not changed.
const PreparedHTTPClientEntrypointsVersion = "yolo.http-client-entrypoints/2"

// PreparedHTTPClientEntrypoint is one selected client's checked delivery and its binding. Before
// the keeper mints a generation the producer hands it over with Binding.Generation empty
// (ValidateDelivery); in a published record the binding names the record's generation.
type PreparedHTTPClientEntrypoint struct {
	Bin            string `json:"bin"`
	Pack           string `json:"pack"`
	Front          string `json:"front"`
	RecipePack     string `json:"recipe_pack"`
	RecipeIdentity string `json:"recipe_identity"`
	// The checked ordinary delivery (entrypoint.ProgramCaptureDelivery) and its audited source.
	Declared        string `json:"declared"`
	Platform        string `json:"platform"`
	Runtime         string `json:"runtime"`
	Compatibility   string `json:"compatibility"`
	Release         string `json:"release"`
	SourceURL       string `json:"source_url"`
	SourceIntegrity string `json:"source_integrity"`
	// Bootstrap is entrypoint.PrivateClientBootstrap, the one program every caller starts.
	Bootstrap string                          `json:"bootstrap"`
	Binding   entrypoint.PrivateClientBinding `json:"binding"`
}

// PreparedHTTPClientEntrypoints is the published record, one per prepared generation.
type PreparedHTTPClientEntrypoints struct {
	Version    string                         `json:"version"`
	Generation string                         `json:"generation"`
	Container  string                         `json:"container"`
	Loophole   string                         `json:"loophole"`
	Clients    []PreparedHTTPClientEntrypoint `json:"clients"`
}

// SameSelection reports whether c names the same selected client as plan.
func (c PreparedHTTPClientEntrypoint) SameSelection(plan PreparedHTTPClientPlan) bool {
	return c.Bin == plan.Bin && c.Pack == plan.Pack && c.Front == plan.Front &&
		c.RecipePack == plan.RecipePack && c.RecipeIdentity == plan.RecipeIdentity
}

// ValidateDelivery checks everything but the generation: a selected npm recipe, the checked
// delivery and audited source, the shipped bootstrap, and a binding of the same bin.
func (c PreparedHTTPClientEntrypoint) ValidateDelivery() error {
	for name, value := range map[string]string{"bin": c.Bin, "pack": c.Pack, "front": c.Front,
		"recipe pack": c.RecipePack, "declared recipe": c.Declared, "platform": c.Platform,
		"runtime": c.Runtime, "release": c.Release} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("private client entrypoint is missing its %s", name)
		}
	}
	if !strings.HasPrefix(c.RecipeIdentity, "sha256:") {
		return errors.New("private client entrypoint has no selected program recipe")
	}
	if b, err := hex.DecodeString(c.Compatibility); err != nil || len(b) != 32 {
		return errors.New("private client entrypoint has no delivery identity")
	}
	if !strings.HasPrefix(c.SourceURL, "https://") || strings.ContainsAny(c.SourceURL, " |\t\r\n") {
		return errors.New("private client entrypoint has no audited source URL")
	}
	digest, ok := strings.CutPrefix(c.SourceIntegrity, "sha512-")
	if raw, err := base64.StdEncoding.DecodeString(digest); !ok || err != nil || len(raw) != 64 {
		return errors.New("private client entrypoint has no audited SHA-512 integrity")
	}
	if c.Bootstrap != entrypoint.PrivateClientBootstrap {
		return errors.New("private client entrypoint does not start through the shipped bootstrap")
	}
	if c.Binding.Bin != c.Bin {
		return errors.New("private client entrypoint binds another bin")
	}
	// The bound Node is the one the checked delivery's runtime observed (pack-brokers-http-plan.md,
	// Task 2's interpreter closure).
	var runtime struct{ NodeDigest string }
	if json.Unmarshal([]byte(c.Runtime), &runtime) != nil || runtime.NodeDigest == "" || c.Binding.NodeSHA256 != "sha256:"+runtime.NodeDigest {
		return errors.New("private client entrypoint does not bind the Node its delivery was prepared with")
	}
	return nil
}

// Validate checks c as part of a published record of generation.
func (c PreparedHTTPClientEntrypoint) Validate(generation string) error {
	if err := c.ValidateDelivery(); err != nil {
		return err
	}
	if c.Binding.Generation != generation {
		return errors.New("private client entrypoint binds another generation")
	}
	return c.Binding.Valid()
}

// Validate checks the record's own shape.
func (r PreparedHTTPClientEntrypoints) Validate() error {
	if r.Version != PreparedHTTPClientEntrypointsVersion || r.Container == "" || r.Loophole == "" || len(r.Clients) == 0 {
		return errors.New("prepared HTTP client entrypoints record is incomplete")
	}
	seen := map[string]bool{}
	for _, c := range r.Clients {
		if err := c.Validate(r.Generation); err != nil {
			return err
		}
		if c.Front != r.Loophole || seen[c.Bin] {
			return errors.New("prepared HTTP client entrypoints record does not match its front")
		}
		seen[c.Bin] = true
	}
	return nil
}

// DecodePreparedHTTPClientEntrypoints reads a record strictly: its bytes must be exactly the
// canonical encoding of a valid record, so a duplicate, unknown, renamed or missing field, a
// reorder or trailing data all refuse.
func DecodePreparedHTTPClientEntrypoints(data []byte) (PreparedHTTPClientEntrypoints, error) {
	var r PreparedHTTPClientEntrypoints
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return PreparedHTTPClientEntrypoints{}, err
	}
	canonical, err := json.Marshal(r)
	if err != nil || !bytes.Equal(canonical, data) {
		return PreparedHTTPClientEntrypoints{}, errors.New("prepared HTTP client entrypoints record is not canonical")
	}
	if err := r.Validate(); err != nil {
		return PreparedHTTPClientEntrypoints{}, err
	}
	return r, nil
}

// ValidatePreparedHTTPClientEntrypoints joins r to the prepared generation g it sits beside:
// the same generation, container and front, and exactly g's clients, in g's order.
func ValidatePreparedHTTPClientEntrypoints(r PreparedHTTPClientEntrypoints, g PreparedHTTPClientGeneration) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := g.Validate(); err != nil {
		return err
	}
	if r.Generation != g.Generation || r.Container != g.Container || r.Loophole != g.Loophole || len(r.Clients) != len(g.Clients) {
		return errors.New("prepared HTTP client entrypoints do not match their prepared generation")
	}
	for i, c := range r.Clients {
		if !c.SameSelection(g.Clients[i]) {
			return fmt.Errorf("prepared HTTP client entrypoint for %q does not match its selected plan", c.Bin)
		}
	}
	return nil
}

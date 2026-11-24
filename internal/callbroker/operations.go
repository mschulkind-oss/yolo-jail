package callbroker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const typedClassifierVersion = "yolo.call-broker-classifier/2"

// Read is the optional typed /2 descriptor. The /1 array-of-chains form remains
// accepted; deriving its effective cell never changes the selected declaration.
type Read struct {
	Kind        string     `json:"kind,omitempty"`
	Cell        string     `json:"cell,omitempty"`
	Chain       []Resource `json:"chain"`
	Description string     `json:"description,omitempty"`
	legacy      bool
}

func NewReadClassification(argv []string, digest string, read Read) *Classification {
	read.Chain = slices.Clone(read.Chain)
	return &Classification{Argv: slices.Clone(argv), StdinSHA256: digest, Reads: [][]Resource{slices.Clone(read.Chain)}, ReadDescriptors: []Read{read}}
}

func decodeReadDescriptors(raw json.RawMessage) ([][]Resource, []Read, error) {
	var entries []json.RawMessage
	if !hasJSONType(raw, '[') || json.Unmarshal(raw, &entries) != nil {
		return nil, nil, errors.New("reads must be an array")
	}
	var chains [][]Resource
	var descriptors []Read
	typed := false
	for _, entry := range entries {
		var descriptor Read
		if hasJSONType(entry, '[') {
			descriptor.legacy = true
			if err := json.Unmarshal(entry, &descriptor.Chain); err != nil {
				return nil, nil, err
			}
		} else {
			typed = true
			fields, err := exactHTTPObject(entry, []string{"chain"}, "kind", "cell", "description")
			if err != nil {
				return nil, nil, err
			}
			for _, key := range []string{"kind", "cell", "description"} {
				if value, has := fields[key]; has && !httpJSONString(value) {
					return nil, nil, errors.New("read descriptor metadata must be strings")
				}
			}
			if !hasJSONType(fields["chain"], '[') {
				return nil, nil, errors.New("read chain must be an array")
			}
			if err := json.Unmarshal(entry, &descriptor); err != nil {
				return nil, nil, err
			}
			if _, has := fields["cell"]; has && descriptor.Cell == "" {
				return nil, nil, errors.New("read cell must be nonempty when supplied")
			}
			if _, has := fields["kind"]; has && descriptor.Kind != "read" {
				return nil, nil, errors.New("read descriptor kind must be read")
			}
		}
		for _, resource := range descriptor.Chain {
			if resource.Type == "" || resource.ID == "" {
				return nil, nil, errors.New("read resource must name type and id")
			}
		}
		var resources []json.RawMessage
		chainRaw := entry
		if hasJSONType(entry, '{') {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(entry, &fields)
			chainRaw = fields["chain"]
		}
		if err := json.Unmarshal(chainRaw, &resources); err != nil {
			return nil, nil, err
		}
		for _, resource := range resources {
			if err := validateHTTPResourceShape(resource); err != nil {
				return nil, nil, err
			}
		}
		chains = append(chains, descriptor.Chain)
		descriptors = append(descriptors, descriptor)
	}
	if !typed {
		descriptors = nil
	}
	return chains, descriptors, nil
}

func (c Classification) MarshalJSON() ([]byte, error) {
	var reads any = c.Reads
	if len(c.ReadDescriptors) != 0 {
		reads = c.ReadDescriptors
	}
	if len(c.Reads)+len(c.ReadDescriptors) == 0 {
		reads = nil
	}
	return json.Marshal(struct {
		Argv        []string `json:"argv"`
		StdinSHA256 string   `json:"stdin_sha256"`
		Reads       any      `json:"reads,omitempty"`
		Writes      []Write  `json:"writes,omitempty"`
	}{c.Argv, c.StdinSHA256, reads, c.Writes})
}

func (c *Classification) UnmarshalJSON(raw []byte) error {
	if err := rejectDuplicateJSONFields(raw); err != nil {
		return err
	}
	fields, err := exactHTTPObject(raw, []string{"argv", "stdin_sha256"}, "reads", "writes")
	if err != nil {
		return err
	}
	var wire struct {
		Argv        []string        `json:"argv"`
		StdinSHA256 string          `json:"stdin_sha256"`
		Reads       json.RawMessage `json:"reads"`
		Writes      []Write         `json:"writes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	*c = Classification{Argv: wire.Argv, StdinSHA256: wire.StdinSHA256, Writes: wire.Writes}
	if _, has := fields["reads"]; has {
		c.Reads, c.ReadDescriptors, err = decodeReadDescriptors(wire.Reads)
	}
	return err
}

// UnmarshalJSON keeps both built-in Run boundaries strict, including duplicate
// keys that encoding/json otherwise accepts. The input vocabulary is unchanged.
func (input *CLIInput) UnmarshalJSON(raw []byte) error {
	if err := rejectDuplicateJSONFields(raw); err != nil {
		return err
	}
	type plainCLIInput CLIInput
	var plain plainCLIInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plain); err != nil {
		return err
	}
	*input = CLIInput(plain)
	return nil
}

func (lookup *LookupRequest) UnmarshalJSON(raw []byte) error {
	fields, err := exactHTTPObject(raw, []string{"name", "arguments"})
	if err != nil {
		return err
	}
	if !httpJSONString(fields["name"]) || !hasJSONType(fields["arguments"], '[') {
		return errors.New("lookup must name a string and an arguments array")
	}
	type plainLookupRequest LookupRequest
	var plain plainLookupRequest
	if err := json.Unmarshal(raw, &plain); err != nil {
		return err
	}
	*lookup = LookupRequest(plain)
	return nil
}

func compatibleClassifierVersion(answer, input string) bool {
	return answer == input || input == "yolo.call-broker-classifier/1" && answer == typedClassifierVersion
}

func (c Classifier) normalizeOperations(call *Classification, version string, provisional, withheldFact bool) error {
	if err := validateOperationWireVersion(version, call.Reads, call.ReadDescriptors, call.Writes); err != nil {
		return err
	}
	var reads []string
	for _, cell := range c.Cells {
		if cell.IsRead() {
			reads = append(reads, cell.Name)
		}
	}
	call.Operations = nil
	for i, chain := range call.Reads {
		if err := c.validateChain(chain); err != nil {
			return err
		}
		descriptor := Read{Chain: chain}
		if len(call.ReadDescriptors) != 0 {
			if len(call.ReadDescriptors) != len(call.Reads) {
				return errors.New("read descriptors do not match their chains")
			}
			descriptor = call.ReadDescriptors[i]
		}
		if descriptor.Kind != "" && descriptor.Kind != "read" {
			return errors.New("invalid read kind")
		}
		if !provisional && descriptor.Cell == "" {
			switch len(reads) {
			case 0:
				if !c.ReadGrant {
					return errors.New("source declares neither a read cell nor a read grant")
				}
			case 1:
				descriptor.Cell = reads[0]
			default:
				return errors.New("multiple read cells require a named read; update the pack's classifier or declare at most one read cell")
			}
		} else if !provisional {
			found := false
			for _, cell := range reads {
				found = found || cell == descriptor.Cell
			}
			if !found {
				return errors.New("read names an undeclared or write cell")
			}
		}
		if descriptor.Description == "" {
			descriptor.Description = fmt.Sprintf("read %s %q", chain[0].Type, chain[0].ID)
			if len(descriptor.Description) > 512 || termsafe.HasUnsafe(descriptor.Description) {
				descriptor.Description = "read selected resource chain"
			}
		}
		if len(descriptor.Description) > 512 || termsafe.HasUnsafe(descriptor.Description) {
			return errors.New("read description is unsafe or oversized")
		}
		if !provisional {
			call.Operations = append(call.Operations, operation(c.Source, brokerpolicy.Read, descriptor.Cell, descriptor.Description, chain))
		}
	}
	for _, write := range call.Writes {
		cells := []string{write.Cell}
		if write.Cell == "" || len(write.Candidates) != 0 {
			if !withheldFact || write.Cell != "" || len(write.Candidates) < 2 {
				return errors.New("a write names one declared cell, or at least two candidates only after a withheld lookup; update the pack's classifier")
			}
			cells = write.Candidates
		}
		if write.Kind != "" && write.Kind != "write" || write.Description == "" || len(write.Description) > 512 || termsafe.HasUnsafe(write.Description) {
			return errors.New("unknown write cell or invalid write description/kind")
		}
		if err := c.validateChain(write.Chain); err != nil {
			return err
		}
		for i, name := range cells {
			found := false
			for _, cell := range c.Cells {
				found = found || cell.Name == name && !cell.IsRead()
			}
			if !found || name == "read" || slices.Contains(cells[:i], name) {
				return errors.New("unknown, repeated or non-write cell")
			}
			if !provisional {
				call.Operations = append(call.Operations, operation(c.Source, brokerpolicy.Write, name, write.Description, write.Chain))
			}
		}
	}
	return nil
}

func operation(source string, kind brokerpolicy.Kind, cell, description string, chain []Resource) brokerpolicy.Operation {
	op := brokerpolicy.Operation{Source: source, Kind: kind, Cell: cell, Description: description}
	for _, resource := range chain {
		op.Chain = append(op.Chain, brokerpolicy.Resource{Type: resource.Type, ID: resource.ID})
	}
	return op
}

func (c *httpClassification) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Canonical httpCanonicalRequest `json:"canonical"`
		Reads     json.RawMessage      `json:"reads"`
		Writes    []Write              `json:"writes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	*c = httpClassification{Canonical: wire.Canonical, Writes: wire.Writes}
	if wire.Reads != nil {
		var err error
		c.Reads, c.ReadDescriptors, err = decodeReadDescriptors(wire.Reads)
		return err
	}
	return nil
}

func (w *Write) UnmarshalJSON(raw []byte) error {
	fields, err := exactHTTPObject(raw, []string{"chain", "description"}, "kind", "cell", "candidates")
	if err != nil {
		return err
	}
	_, hasCell := fields["cell"]
	_, hasCandidates := fields["candidates"]
	if hasCell == hasCandidates {
		return errors.New("write must name exactly one of cell or candidates")
	}
	if hasCandidates && !hasJSONType(fields["candidates"], '[') {
		return errors.New("write candidates must be an array")
	}
	for _, key := range []string{"cell", "description", "kind"} {
		if value, has := fields[key]; has && !httpJSONString(value) {
			return errors.New("write metadata must be strings")
		}
	}
	if !hasJSONType(fields["chain"], '[') {
		return errors.New("write chain must be an array")
	}
	type wireWrite Write
	var wire wireWrite
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if _, has := fields["kind"]; has && wire.Kind != "write" {
		return errors.New("write descriptor kind must be write")
	}
	var resources []json.RawMessage
	if err := json.Unmarshal(fields["chain"], &resources); err != nil {
		return err
	}
	for _, resource := range resources {
		if err := validateHTTPResourceShape(resource); err != nil {
			return err
		}
	}
	*w = Write(wire)
	_, w.kindPresent = fields["kind"]
	return nil
}

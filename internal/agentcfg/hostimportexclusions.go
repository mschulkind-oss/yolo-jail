package agentcfg

import (
	"fmt"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
)

// Architecture and invariants: docs/reference/host-import-exclusions.md

// FilterHostImportExclusions removes exact object members from one imported
// host configuration document. An empty pointer list leaves the original bytes
// untouched; active filters fail closed if the codec cannot process the object.
func FilterHostImportExclusions(codecName string, host []byte, pointers []string) ([]byte, error) {
	if len(pointers) == 0 {
		return host, nil
	}

	kind, known := codec.KindOf(codecName)
	if !known {
		return nil, fmt.Errorf("unknown host-import codec %q", codecName)
	}
	if kind != codec.KindObject {
		return nil, fmt.Errorf("host-import exclusions require an object codec; %q is %s", codecName, kind)
	}
	c, ok := codec.LookupCodec(codecName)
	if !ok {
		return nil, fmt.Errorf("unknown host-import codec %q", codecName)
	}
	return filterHostImportExclusions(c, host, pointers)
}

func filterHostImportExclusions(c codec.Codec, host []byte, pointers []string) ([]byte, error) {
	if len(pointers) == 0 {
		return host, nil
	}

	type exclusion struct {
		pointer string
		tokens  []string
	}
	parsed := make([]exclusion, 0, len(pointers))
	seen := make(map[string]struct{}, len(pointers))
	for _, pointer := range pointers {
		if _, ok := seen[pointer]; ok {
			continue
		}
		seen[pointer] = struct{}{}
		tokens, err := jsonptr.Parse(pointer)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 0 {
			return nil, fmt.Errorf("host-import exclusion %q cannot target the document root", pointer)
		}
		parsed = append(parsed, exclusion{pointer: pointer, tokens: tokens})
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].pointer < parsed[j].pointer })

	decoded, err := c.Decode(host)
	if err != nil {
		return nil, fmt.Errorf("decode host configuration for exclusions: %w", err)
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("host configuration for exclusions decoded to %T, want object", decoded)
	}

	changed := false
	for _, exclusion := range parsed {
		object := root
		for _, token := range exclusion.tokens[:len(exclusion.tokens)-1] {
			child, exists := object[token]
			if !exists {
				object = nil
				break
			}
			next, isObject := child.(map[string]any)
			if !isObject {
				object = nil
				break
			}
			object = next
		}
		if object == nil {
			continue
		}
		last := exclusion.tokens[len(exclusion.tokens)-1]
		if _, exists := object[last]; exists {
			delete(object, last)
			changed = true
		}
	}
	if !changed {
		return host, nil
	}
	filtered, err := c.Encode(root)
	if err != nil {
		return nil, fmt.Errorf("encode host configuration after exclusions: %w", err)
	}
	return filtered, nil
}

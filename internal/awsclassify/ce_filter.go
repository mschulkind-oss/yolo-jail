package awsclassify

import (
	"unicode"
	"unicode/utf8"
)

var ceMatchOptions = []string{"EQUALS", "ABSENT", "STARTS_WITH", "ENDS_WITH", "CONTAINS", "CASE_SENSITIVE", "CASE_INSENSITIVE", "GREATER_THAN_OR_EQUAL"}

type ceLeaf struct {
	Key          string
	Values       []string
	MatchOptions []string `json:",omitempty"`
}

func ceFilter(s string) (string, bool) {
	if len(s) > 8192 {
		return "", false
	}
	v, err := ceJSON(s)
	if err != nil {
		return "", false
	}
	leaves := 0
	node, ok := ceFilterNode(v, 1, &leaves)
	if !ok {
		return "", false
	}
	return ceCompact(node), true
}

func ceFilterNode(v any, depth int, leaves *int) (any, bool) {
	if depth > 5 {
		return nil, false
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, false
	}
	for key, value := range m {
		switch key {
		case "And", "Or":
			a, ok := value.([]any)
			if !ok || len(a) < 2 {
				return nil, false
			}
			children := []any{}
			for _, child := range a {
				node, ok := ceFilterNode(child, depth+1, leaves)
				if !ok {
					return nil, false
				}
				children = append(children, node)
			}
			return map[string]any{key: children}, true
		case "Not":
			node, ok := ceFilterNode(value, depth+1, leaves)
			if !ok {
				return nil, false
			}
			return map[string]any{key: node}, true
		case "Dimensions", "Tags", "CostCategories":
			*leaves++
			if *leaves > 50 {
				return nil, false
			}
			leaf, ok := value.(map[string]any)
			if !ok || len(leaf) < 2 || len(leaf) > 3 {
				return nil, false
			}
			for k := range leaf {
				if k != "Key" && k != "Values" && k != "MatchOptions" {
					return nil, false
				}
			}
			k, ok := leaf["Key"].(string)
			if !ok {
				return nil, false
			}
			if key == "Dimensions" {
				if !ceDimension.MatchString(k) {
					return nil, false
				}
			} else if !ceLabel.MatchString(k) {
				return nil, false
			}
			values, ok := leaf["Values"].([]any)
			if !ok || len(values) < 1 || len(values) > 100 {
				return nil, false
			}
			out := ceLeaf{Key: k, Values: []string{}}
			seen := map[string]bool{}
			for _, v := range values {
				s, ok := v.(string)
				if !ok || len(s) > 256 || !utf8.ValidString(s) || seen[s] {
					return nil, false
				}
				for _, r := range s {
					if unicode.IsControl(r) {
						return nil, false
					}
				}
				seen[s] = true
				out.Values = append(out.Values, s)
			}
			if raw, present := leaf["MatchOptions"]; present {
				a, ok := raw.([]any)
				if !ok {
					return nil, false
				}
				options := []string{}
				for _, v := range a {
					s, ok := v.(string)
					if !ok {
						return nil, false
					}
					options = append(options, s)
				}
				sorted, ok := ceEnumList(options, ceMatchOptions, 8)
				if !ok {
					return nil, false
				}
				out.MatchOptions = sorted
			}
			return map[string]any{key: out}, true
		}
	}
	return nil, false
}

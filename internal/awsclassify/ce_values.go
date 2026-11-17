package awsclassify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ceScheme    = regexp.MustCompile(`^[A-Za-z0-9+.-]+://`)
	ceDimension = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	ceLabel     = regexp.MustCompile(`^[A-Za-z0-9 _.:/=+@-]{1,256}$`)
	ceDate      = regexp.MustCompile(`^20[0-9]{2}-[0-9]{2}-[0-9]{2}$`)
	ceDecimal   = regexp.MustCompile(`^[1-9][0-9]*$`)
)
var ceMetrics = []string{"AmortizedCost", "BlendedCost", "NetAmortizedCost", "NetUnblendedCost", "NormalizedUsageAmount", "UnblendedCost", "UsageQuantity"}
var ceForecastMetrics = []string{"AMORTIZED_COST", "BLENDED_COST", "NET_AMORTIZED_COST", "NET_UNBLENDED_COST", "UNBLENDED_COST"}

func ceBoundary(s string) bool { return !strings.HasPrefix(s, "-") && !ceScheme.MatchString(s) }

func ceValue(name string, raw []string) ([]string, bool) {
	if name == "metrics" {
		return ceEnumList(raw, ceMetrics, 7)
	}
	if name == "group-by" {
		s, ok := ceGroups(raw)
		return []string{s}, ok
	}
	if len(raw) != 1 {
		return nil, false
	}
	s := raw[0]
	ok := false
	switch name {
	case "time-period":
		s, ok = cePeriod(s)
	case "granularity":
		ok = s == "DAILY" || s == "MONTHLY"
	case "metric":
		for _, v := range ceForecastMetrics {
			ok = ok || s == v
		}
	case "dimension":
		ok = ceDimension.MatchString(s)
	case "filter":
		s, ok = ceFilter(s)
	case "search-string", "tag-key":
		ok = ceLabel.MatchString(s)
	case "max-results":
		ok = ceNumber(s, 1, 1000)
	case "prediction-interval-level":
		ok = ceNumber(s, 51, 99)
	case "next-page-token":
		ok = len(s) >= 1 && len(s) <= 8192
		for i := 0; i < len(s); i++ {
			ok = ok && s[i] >= 33 && s[i] <= 126
		}
	case "context":
		ok = s == "COST_AND_USAGE"
	}
	return []string{s}, ok
}

func ceNumber(s string, min, max int) bool {
	if !ceDecimal.MatchString(s) {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= min && n <= max
}

func ceEnumList(raw, order []string, max int) ([]string, bool) {
	if len(raw) < 1 || len(raw) > max {
		return nil, false
	}
	seen := map[string]bool{}
	for _, v := range raw {
		if seen[v] {
			return nil, false
		}
		seen[v] = true
	}
	out := []string{}
	for _, v := range order {
		if seen[v] {
			out = append(out, v)
			delete(seen, v)
		}
	}
	return out, len(seen) == 0
}

func cePeriod(s string) (string, bool) {
	fields := map[string]string{}
	if strings.HasPrefix(s, "{") {
		v, err := ceJSON(s)
		if err != nil {
			return "", false
		}
		m, ok := v.(map[string]any)
		if !ok || len(m) != 2 {
			return "", false
		}
		for k, v := range m {
			value, ok := v.(string)
			if !ok {
				return "", false
			}
			fields[k] = value
		}
	} else {
		parts := strings.Split(s, ",")
		if len(parts) != 2 {
			return "", false
		}
		for _, p := range parts {
			k, v, ok := strings.Cut(p, "=")
			if !ok {
				return "", false
			}
			if _, exists := fields[k]; exists {
				return "", false
			}
			fields[k] = v
		}
	}
	start, end := fields["Start"], fields["End"]
	if len(fields) != 2 || !ceDate.MatchString(start) || !ceDate.MatchString(end) {
		return "", false
	}
	a, err := time.Parse("2006-01-02", start)
	if err != nil {
		return "", false
	}
	b, err := time.Parse("2006-01-02", end)
	if err != nil || !a.Before(b) {
		return "", false
	}
	return "Start=" + start + ",End=" + end, true
}

type ceGroup struct {
	Type string
	Key  string
}

func ceGroups(raw []string) (string, bool) {
	groups := []ceGroup{}
	if strings.HasPrefix(raw[0], "[") {
		if len(raw) != 1 {
			return "", false
		}
		v, err := ceJSON(raw[0])
		if err != nil {
			return "", false
		}
		list, ok := v.([]any)
		if !ok {
			return "", false
		}
		for _, v := range list {
			m, ok := v.(map[string]any)
			if !ok || len(m) != 2 {
				return "", false
			}
			typ, tok := m["Type"].(string)
			key, kok := m["Key"].(string)
			if !tok || !kok {
				return "", false
			}
			groups = append(groups, ceGroup{typ, key})
		}
	} else {
		for _, s := range raw {
			if !strings.HasPrefix(s, "Type=") {
				return "", false
			}
			typ, key, ok := strings.Cut(strings.TrimPrefix(s, "Type="), ",Key=")
			if !ok || strings.ContainsAny(key, ",=") {
				return "", false
			}
			groups = append(groups, ceGroup{typ, key})
		}
	}
	if len(groups) < 1 || len(groups) > 2 {
		return "", false
	}
	seen := map[ceGroup]bool{}
	for _, g := range groups {
		if seen[g] {
			return "", false
		}
		seen[g] = true
		switch g.Type {
		case "DIMENSION":
			if !ceDimension.MatchString(g.Key) {
				return "", false
			}
		case "TAG", "COST_CATEGORY":
			if !ceLabel.MatchString(g.Key) {
				return "", false
			}
		default:
			return "", false
		}
	}
	return ceCompact(groups), true
}

func ceCompact(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

// ceJSON checks duplicates at every object level before interpreting the row's schema.
// Token depth is only a parser guard; filter policy counts logical nodes separately.
func ceJSON(s string) (any, error) {
	if !utf8.ValidString(s) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	v, err := ceJSONValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return v, nil
}
func ceJSONValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("JSON depth exceeded")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("invalid key")
			}
			if _, exists := m[key]; exists {
				return nil, fmt.Errorf("duplicate key")
			}
			v, err := ceJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = v
		}
		close, err := d.Token()
		if err != nil || close != json.Delim('}') {
			return nil, fmt.Errorf("invalid object")
		}
		return m, nil
	case '[':
		a := []any{}
		for d.More() {
			v, err := ceJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		close, err := d.Token()
		if err != nil || close != json.Delim(']') {
			return nil, fmt.Errorf("invalid array")
		}
		return a, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter")
	}
}

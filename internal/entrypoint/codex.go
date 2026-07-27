package entrypoint

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// isJSONInt reports whether v is a jsonx integer literal (re-encodes without a
// decimal point). jsonx keeps integers as an unexported jsonInt type; we detect
// it by round-tripping through DumpsCompact (an int has no '.'/'e').
func isJSONInt(v any) bool {
	switch v.(type) {
	case float64, bool, string:
		return false
	}
	s, err := jsonx.DumpsCompact(v)
	if err != nil {
		return false
	}
	return !strings.ContainsAny(s, ".eE") && looksLikeNumber(s)
}

func looksLikeNumber(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

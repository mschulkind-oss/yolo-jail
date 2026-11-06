package callbroker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// validateHTTPBrokerFrame runs on authenticated, bounded HTTP ingress before
// hostservice constructs a Session. encoding/json matches struct field names
// case-insensitively; only these exact original spellings may reach HTTPCall.
// Generic CLI/Git/GitHub frames do not use this validator.
func validateHTTPBrokerFrame(data []byte) error {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(data), &probe); err != nil {
		return err
	}
	var op string
	if raw, ok := probe["op"]; ok && httpJSONString(raw) {
		if err := json.Unmarshal(raw, &op); err != nil {
			return err
		}
	}
	if op == httpFrontSealOp {
		top, err := exactHTTPObject(data, []string{"op", "front_generation", "front"}, "jail_id")
		if err != nil {
			return err
		}
		if !httpJSONString(top["front_generation"]) || top["jail_id"] != nil && !httpJSONString(top["jail_id"]) {
			return errors.New("HTTP front seal generation or jail hint has the wrong type")
		}
		front, err := exactHTTPObject(top["front"], []string{"address", "certificate_base64"})
		if err != nil {
			return err
		}
		if !httpJSONString(front["address"]) || !httpJSONString(front["certificate_base64"]) {
			return errors.New("HTTP front seal address and certificate must be strings")
		}
		return nil
	}
	top, err := exactHTTPObject(data, []string{"op", "http"}, "jail_id", "front_generation")
	if err != nil {
		return err
	}
	if !httpJSONString(top["op"]) || top["jail_id"] != nil && !httpJSONString(top["jail_id"]) ||
		top["front_generation"] != nil && !httpJSONString(top["front_generation"]) {
		return errors.New("HTTP broker operation, jail hint or front generation has the wrong type")
	}
	call, err := exactHTTPObject(top["http"], []string{"method", "path", "query", "headers", "body_present", "body_base64"})
	if err != nil {
		return err
	}
	for _, field := range []string{"method", "path", "query"} {
		if !httpJSONString(call[field]) {
			return fmt.Errorf("original HTTP %s must be a string", field)
		}
	}
	if body := bytes.TrimSpace(call["body_present"]); !bytes.Equal(body, []byte("true")) && !bytes.Equal(body, []byte("false")) {
		return errors.New("original HTTP body_present must be a boolean")
	}
	if body := bytes.TrimSpace(call["body_base64"]); !bytes.Equal(body, []byte("null")) && !httpJSONString(body) {
		return errors.New("original HTTP body_base64 must be a string or null")
	}
	headers := bytes.TrimSpace(call["headers"])
	if bytes.Equal(headers, []byte("null")) {
		return nil
	}
	if len(headers) == 0 || headers[0] != '[' {
		return errors.New("original HTTP headers must be an array or null")
	}
	var list []json.RawMessage
	if err := json.Unmarshal(headers, &list); err != nil {
		return err
	}
	for _, raw := range list {
		header, err := exactHTTPObject(raw, []string{"name", "values"})
		if err != nil {
			return err
		}
		if !httpJSONString(header["name"]) {
			return errors.New("original HTTP header name must be a string")
		}
		values := bytes.TrimSpace(header["values"])
		if bytes.Equal(values, []byte("null")) {
			continue
		}
		if len(values) == 0 || values[0] != '[' {
			return errors.New("original HTTP header values must be an array or null")
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(values, &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			if !httpJSONString(entry) {
				return errors.New("original HTTP header values must be strings")
			}
		}
	}
	return nil
}

func exactHTTPObject(data json.RawMessage, required []string, optional ...string) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("original HTTP object is missing or not an object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, err
	}
	allowed := append(append([]string(nil), required...), optional...)
	if err := checkExactFields(fields, allowed...); err != nil {
		return nil, err
	}
	for _, field := range required {
		if _, ok := fields[field]; !ok {
			return nil, fmt.Errorf("original HTTP field %q is missing", field)
		}
	}
	return fields, nil
}

func httpJSONString(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) != 0 && trimmed[0] == '"'
}

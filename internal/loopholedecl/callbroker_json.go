package loopholedecl

import "encoding/json"

// callBrokerDuplicateKey finds duplicate JSONC keys in the call_broker subtree before the
// ordered-map decoder can collapse them. Other legacy manifest objects keep their existing
// duplicate-key behavior.
func callBrokerDuplicateKey(data []byte) (string, bool) {
	p := callBrokerJSONScanner{data: data}
	key, ok := p.value("")
	return key, ok
}

type callBrokerJSONScanner struct {
	data []byte
	at   int
}

func (p *callBrokerJSONScanner) space() {
	for p.at < len(p.data) {
		switch p.data[p.at] {
		case ' ', '\t', '\r', '\n':
			p.at++
		case '/':
			if p.at+1 >= len(p.data) {
				return
			}
			switch p.data[p.at+1] {
			case '/':
				p.at += 2
				for p.at < len(p.data) && p.data[p.at] != '\n' {
					p.at++
				}
			case '*':
				p.at += 2
				for p.at+1 < len(p.data) && (p.data[p.at] != '*' || p.data[p.at+1] != '/') {
					p.at++
				}
				if p.at+1 < len(p.data) {
					p.at += 2
				}
			default:
				return
			}
		default:
			return
		}
	}
}

func (p *callBrokerJSONScanner) value(path string) (string, bool) {
	p.space()
	if p.at >= len(p.data) {
		return "", false
	}
	switch p.data[p.at] {
	case '{':
		return p.object(path)
	case '[':
		return p.array(path)
	case '"':
		_, _ = p.string()
		return "", false
	default:
		for p.at < len(p.data) {
			c := p.data[p.at]
			if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				break
			}
			p.at++
		}
		return "", false
	}
}

func (p *callBrokerJSONScanner) object(path string) (string, bool) {
	p.at++
	seen := map[string]bool{}
	p.space()
	if p.at < len(p.data) && p.data[p.at] == '}' {
		p.at++
		return "", false
	}
	for p.at < len(p.data) {
		p.space()
		if p.at >= len(p.data) || p.data[p.at] != '"' {
			return "", false
		}
		key, ok := p.string()
		if !ok {
			return "", false
		}
		if seen[key] && (path == "" && key == "call_broker" || path == "call_broker" || len(path) > len("call_broker.") && path[:len("call_broker.")] == "call_broker.") {
			return path + "." + key, true
		}
		seen[key] = true
		p.space()
		if p.at >= len(p.data) || p.data[p.at] != ':' {
			return "", false
		}
		p.at++
		child := key
		if path != "" {
			child = path + "." + key
		}
		if child == "call_broker" || len(child) > len("call_broker.") && child[:len("call_broker.")] == "call_broker." {
			if duplicate, found := p.value(child); found {
				return duplicate, true
			}
		} else {
			_, _ = p.value(child)
		}
		p.space()
		if p.at >= len(p.data) {
			return "", false
		}
		if p.data[p.at] == '}' {
			p.at++
			return "", false
		}
		if p.data[p.at] != ',' {
			return "", false
		}
		p.at++
	}
	return "", false
}

func (p *callBrokerJSONScanner) array(path string) (string, bool) {
	p.at++
	p.space()
	if p.at < len(p.data) && p.data[p.at] == ']' {
		p.at++
		return "", false
	}
	for p.at < len(p.data) {
		if duplicate, found := p.value(path); found {
			return duplicate, true
		}
		p.space()
		if p.at >= len(p.data) {
			return "", false
		}
		if p.data[p.at] == ']' {
			p.at++
			return "", false
		}
		if p.data[p.at] != ',' {
			return "", false
		}
		p.at++
	}
	return "", false
}

func (p *callBrokerJSONScanner) string() (string, bool) {
	start := p.at
	p.at++
	for p.at < len(p.data) {
		if p.data[p.at] == '\\' {
			p.at += 2
			continue
		}
		if p.data[p.at] == '"' {
			p.at++
			var decoded string
			if err := json.Unmarshal(p.data[start:p.at], &decoded); err != nil {
				return "", false
			}
			return decoded, true
		}
		p.at++
	}
	return "", false
}

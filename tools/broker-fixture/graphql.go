package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type gqlToken struct{ kind, text string }
type gqlField struct {
	name, alias string
	args        map[string]string
	children    []gqlField
}
type gqlOperation struct {
	kind, name string
	fields     []gqlField
}
type gqlParser struct {
	tokens []gqlToken
	pos    int
}

func lexGraphQL(src []byte) ([]gqlToken, error) {
	if len(src) == 0 || len(src) > 1<<20 {
		return nil, errors.New("GraphQL fixture document must be 1 byte to 1 MiB")
	}
	var out []gqlToken
	for i := 0; i < len(src); {
		c := src[i]
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == ',' {
			i++
			continue
		}
		if c == '#' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if strings.ContainsRune("{}():", rune(c)) {
			out = append(out, gqlToken{string(c), string(c)})
			i++
			continue
		}
		if c == '"' {
			start := i
			i++
			for i < len(src) && src[i] != '"' {
				if src[i] == '\\' {
					i++
				}
				i++
			}
			if i >= len(src) {
				return nil, errors.New("unterminated GraphQL string")
			}
			raw := string(src[start : i+1])
			v, e := strconv.Unquote(raw)
			if e != nil {
				return nil, errors.New("invalid GraphQL string")
			}
			out = append(out, gqlToken{"string", v})
			i++
			continue
		}
		if nameStart(c) {
			j := i + 1
			for j < len(src) && namePart(src[j]) {
				j++
			}
			out = append(out, gqlToken{"name", string(src[i:j])})
			i = j
			continue
		}
		return nil, fmt.Errorf("unsupported GraphQL token %q", c)
	}
	if len(out) > 4096 {
		return nil, errors.New("GraphQL fixture token limit exceeded")
	}
	return out, nil
}
func nameStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func namePart(c byte) bool  { return nameStart(c) || c >= '0' && c <= '9' }
func parseGraphQL(src []byte, operationName string) (gqlOperation, error) {
	tokens, e := lexGraphQL(src)
	if e != nil {
		return gqlOperation{}, e
	}
	p := gqlParser{tokens: tokens}
	var ops []gqlOperation
	for p.pos < len(p.tokens) {
		kind, name := "query", ""
		if p.peek("name") {
			switch p.tokens[p.pos].text {
			case "query", "mutation", "subscription":
				kind = p.tokens[p.pos].text
				p.pos++
				if p.peek("name") {
					name = p.tokens[p.pos].text
					p.pos++
				}
			default:
				return gqlOperation{}, fmt.Errorf("unsupported GraphQL operation %q", p.tokens[p.pos].text)
			}
		}
		if kind == "subscription" {
			return gqlOperation{}, errors.New("subscriptions are unsupported by the local fixture")
		}
		fields, e := p.selection(0)
		if e != nil {
			return gqlOperation{}, e
		}
		ops = append(ops, gqlOperation{kind, name, fields})
		if len(ops) > 16 {
			return gqlOperation{}, errors.New("too many GraphQL operations")
		}
	}
	for _, op := range ops {
		if e := validateGQL(op.kind, op.fields, ""); e != nil {
			return gqlOperation{}, e
		}
	}
	var chosen *gqlOperation
	if operationName != "" {
		for i := range ops {
			if ops[i].name == operationName {
				if chosen != nil {
					return gqlOperation{}, errors.New("duplicate GraphQL operation name")
				}
				chosen = &ops[i]
			}
		}
		if chosen == nil {
			return gqlOperation{}, errors.New("unknown operationName")
		}
	} else {
		if len(ops) != 1 {
			return gqlOperation{}, errors.New("ambiguous GraphQL document: supply operationName")
		}
		chosen = &ops[0]
	}
	if chosen.name == "" {
		chosen.name = "anonymous"
	}
	return *chosen, nil
}
func (p *gqlParser) peek(k string) bool { return p.pos < len(p.tokens) && p.tokens[p.pos].kind == k }
func (p *gqlParser) take(k string) (string, error) {
	if !p.peek(k) {
		got := "end of input"
		if p.pos < len(p.tokens) {
			got = p.tokens[p.pos].kind + ":" + p.tokens[p.pos].text
		}
		return "", fmt.Errorf("invalid GraphQL fixture syntax: expected %s, got %s", k, got)
	}
	v := p.tokens[p.pos].text
	p.pos++
	return v, nil
}
func (p *gqlParser) selection(depth int) ([]gqlField, error) {
	if depth > 12 {
		return nil, errors.New("GraphQL selection nesting limit exceeded")
	}
	if _, e := p.take("{"); e != nil {
		return nil, e
	}
	var fields []gqlField
	for !p.peek("}") {
		if p.pos >= len(p.tokens) {
			return nil, errors.New("unterminated GraphQL selection")
		}
		first, e := p.take("name")
		if e != nil {
			return nil, e
		}
		f := gqlField{name: first, alias: first, args: map[string]string{}}
		if p.peek(":") {
			p.pos++
			f.name, e = p.take("name")
			if e != nil {
				return nil, e
			}
		}
		if p.peek("(") {
			p.pos++
			for !p.peek(")") {
				key, e := p.take("name")
				if e != nil {
					return nil, e
				}
				if _, e = p.take(":"); e != nil {
					return nil, e
				}
				if !p.peek("name") && !p.peek("string") {
					return nil, errors.New("GraphQL arguments must be literal names or strings")
				}
				value := p.tokens[p.pos].text
				p.pos++
				if _, ok := f.args[key]; ok {
					return nil, errors.New("duplicate GraphQL argument")
				}
				f.args[key] = value
				if len(f.args) > 4 {
					return nil, errors.New("too many GraphQL arguments")
				}
			}
			p.pos++
		}
		if p.peek("{") {
			f.children, e = p.selection(depth + 1)
			if e != nil {
				return nil, e
			}
		}
		fields = append(fields, f)
		if len(fields) > 128 {
			return nil, errors.New("GraphQL selection field limit exceeded")
		}
	}
	p.pos++
	if len(fields) == 0 {
		return nil, errors.New("empty GraphQL selection")
	}
	return fields, nil
}
func validateGQL(kind string, fields []gqlField, parent string) error {
	for _, f := range fields {
		allowed := false
		switch parent {
		case "":
			allowed = kind == "query" && (f.name == "project" || f.name == "item") || kind == "mutation" && f.name == "updateWorkItem"
		case "project":
			allowed = f.name == "name" || f.name == "items"
		case "item", "updateWorkItem", "items":
			allowed = f.name == "id" || f.name == "state"
		}
		if !allowed {
			return fmt.Errorf("unknown GraphQL fixture field %q", f.name)
		}
		wantArgs := map[string]bool{}
		switch f.name {
		case "project", "item":
			wantArgs["id"] = true
		case "updateWorkItem":
			wantArgs["id"] = true
			wantArgs["state"] = true
		}
		for k := range f.args {
			if !wantArgs[k] {
				return fmt.Errorf("unknown GraphQL fixture argument %q", k)
			}
		}
		for k := range wantArgs {
			if f.args[k] == "" {
				return fmt.Errorf("field %q requires argument %q", f.name, k)
			}
		}
		object := f.name == "project" || f.name == "item" || f.name == "updateWorkItem" || f.name == "items"
		if object && len(f.children) == 0 {
			return fmt.Errorf("field %q requires a selection", f.name)
		}
		if !object && len(f.children) > 0 {
			return fmt.Errorf("scalar field %q cannot have a selection", f.name)
		}
		if f.name == "project" && f.args["id"] != "alpha" && f.args["id"] != "beta" {
			return errors.New("project id must be alpha or beta")
		}
		if f.name == "item" && f.args["id"] != "a1" && f.args["id"] != "b1" {
			return errors.New("item id must be a1 or b1")
		}
		if f.name == "updateWorkItem" && (f.args["id"] != "a1" && f.args["id"] != "b1") {
			return errors.New("updateWorkItem id must be a1 or b1")
		}
		if e := validateGQL(kind, f.children, f.name); e != nil {
			return e
		}
	}
	return nil
}

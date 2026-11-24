package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func hasScopedBrokerVocabulary(values ...*jsonx.OrderedMap) bool {
	var visit func(*jsonx.OrderedMap) bool
	visit = func(table *jsonx.OrderedMap) bool {
		if table == nil {
			return false
		}
		for _, key := range table.Keys() {
			value := readableValue(table, key)
			if key == "never" || key == "scopes" || key == "contexts" {
				return true
			}
			if key == "read" {
				if _, ok := asMap(value); ok || value == "ask" {
					return true
				}
			}
			if child, ok := asMap(value); ok && visit(child) {
				return true
			}
		}
		return false
	}
	for _, value := range values {
		if visit(value) {
			return true
		}
	}
	return false
}

// Only globally new roots are refused here. New broker-owned lines are refused
// with their selected declaration and provenance, so unrelated legacy settings
// named read or never keep their old handling.
func refuseLegacyScopedBrokerVocabulary(values *jsonx.OrderedMap, label string) error {
	for _, key := range []string{"scopes", "contexts"} {
		if _, has := values.Get(key); has {
			return fmt.Errorf("%s: config.%s is TOML-only; use yolo config migrate, then yolo config set", label, key)
		}
	}
	return nil
}

func hasDeclarationScopedVocabulary(declarations []ReadableBrokerDeclaration, values ...*jsonx.OrderedMap) bool {
	optionalValue := func(table *jsonx.OrderedMap, key string) any {
		if table == nil {
			return nil
		}
		return readableValue(table, key)
	}
	for _, declaration := range declarations {
		if declaration.CallBroker != nil {
			for _, value := range values {
				if value == nil {
					continue
				}
				allows, _ := readableTable(value, "allow")
				own, _ := asMap(readableValue(allows, declaration.CallBroker.Source))
				for _, field := range []string{"always", "on_request", "never"} {
					for _, token := range stringList(optionalValue(own, field)) {
						if strings.HasPrefix(token, "read.") {
							return true
						}
					}
				}
			}
			continue
		}
		if declaration.ReadCapability == "" {
			continue
		}
		source, read := declaration.Brokered.Source, declaration.ReadCapability
		hasReadList := func(table *jsonx.OrderedMap) bool {
			for _, field := range []string{"always", "on_request", "never"} {
				for _, token := range stringList(optionalValue(table, field)) {
					if token == read || token == "read" {
						return true
					}
				}
			}
			return false
		}
		hasReadAsk := func(table *jsonx.OrderedMap) bool {
			if table == nil {
				return false
			}
			_, has := table.Get("read")
			_, native := table.Get(read)
			return has || native
		}
		for _, value := range values {
			if value == nil {
				continue
			}
			allows, _ := readableTable(value, "allow")
			own, _ := asMap(readableValue(allows, source))
			requests, _ := readableTable(value, "requests")
			ownRequest, _ := asMap(readableValue(requests, source))
			if optionalValue(ownRequest, read) == "ask" {
				return true
			}
			if read != "read" {
				for _, field := range []string{"always", "on_request", "never"} {
					for _, token := range stringList(optionalValue(own, field)) {
						if token == "read" {
							return true
						}
					}
				}
				asks, _ := readableTable(value, "requests")
				ownAsk, _ := asMap(readableValue(asks, source))
				if ownAsk != nil {
					if _, has := ownAsk.Get("read"); has {
						return true
					}
				}
			}
			repos, _ := readableTable(value, "repos")
			for _, name := range repos.Keys() {
				repo, _ := asMap(readableValue(repos, name))
				allow, _ := asMap(optionalValue(repo, "allow"))
				namedAllow, _ := asMap(optionalValue(allow, source))
				if hasReadList(namedAllow) || declaration.Brokered.RemoteHost == brokerscope.AnyRemoteHost && hasReadList(allow) {
					return true
				}
				asks, _ := asMap(optionalValue(repo, "requests"))
				namedAsk, _ := asMap(optionalValue(asks, source))
				if hasReadAsk(namedAsk) || declaration.Brokered.RemoteHost == brokerscope.AnyRemoteHost && hasReadAsk(asks) {
					return true
				}
			}
		}
	}
	return false
}

func policyListOriginPath(path string) string {
	if strings.HasSuffix(path, "]") {
		if i := strings.LastIndexByte(path, '['); i >= 0 {
			if _, err := strconv.Atoi(path[i+1 : len(path)-1]); err == nil {
				return path[:i]
			}
		}
	}
	return path
}

func refuseLegacyBrokerPolicyLine(path string, origins map[string][]string) error {
	for _, file := range origins[policyListOriginPath(path)] {
		if strings.HasSuffix(file, ".jsonc") || strings.HasSuffix(file, ".json") {
			return fmt.Errorf("%s: config.%s is TOML-only; use yolo config migrate, then yolo config set", file, path)
		}
	}
	return nil
}

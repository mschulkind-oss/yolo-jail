package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// NativeReadScope retains TOML read policy separately from the shipped native
// write-grant projection. Consumers bind repository admission in later units.
type NativeReadScope struct {
	Name, Source, Repository, Layer, ReadWord, Request string
	Always, OnRequest, Never                           bool
	Origins                                            map[string][]string
}

func anyHostReadPolicy(brokers map[string]ReadableBrokerPolicy) (ReadableBrokerPolicy, error) {
	var policy ReadableBrokerPolicy
	for _, candidate := range brokers {
		if candidate.CallBroker != nil || candidate.RemoteHost != brokerscope.AnyRemoteHost {
			continue
		}
		if policy.Source != "" {
			return policy, fmt.Errorf("named native read is ambiguous between %s and %s; select one owning source", policy.Source, candidate.Source)
		}
		policy = candidate
	}
	if policy.Source == "" {
		return policy, fmt.Errorf("named native read has no selected any-host declaration; select its pack before using yolo config set")
	}
	return policy, nil
}

func nativeReadLists(plan *ReadableAllowancePlan, name, line, path string, table *jsonx.OrderedMap, policy ReadableBrokerPolicy, origins map[string][]string) error {
	for _, field := range []string{"always", "on_request", "never"} {
		if err := nativeReadLegacyAliasRefusal(path+"."+field, policy, origins); err != nil {
			return err
		}
	}
	if err := validateScopedCeilingLists(path, table, policy, origins); err != nil {
		return err
	}
	read := NativeReadScope{Name: name, Source: policy.Source, Repository: line, Layer: "ceiling", ReadWord: policy.ReadCapability, Origins: map[string][]string{}}
	for _, field := range []string{"always", "on_request", "never"} {
		tokens, err := readableStringList(readableValue(table, field), path+"."+field)
		if _, has := table.Get(field); !has {
			continue
		}
		if err != nil {
			return err
		}
		for _, token := range tokens {
			if token != "read" && token != policy.ReadCapability {
				return fmt.Errorf("%s.%s: native named read accepts only %s; other Git lines remain unbuilt", path, field, policy.ReadCapability)
			}
		}
		if len(tokens) > 1 {
			return fmt.Errorf("%s.%s: read is listed twice; use yolo config set", path, field)
		}
		holds := len(tokens) == 1
		switch field {
		case "always":
			read.Always = holds
		case "on_request":
			read.OnRequest = holds
		case "never":
			read.Never = holds
		}
		read.Origins[field] = cloneOrigins(origins[path+"."+field])
	}
	plan.NativeReads = append(plan.NativeReads, read)
	return nil
}

func retainNativeSourceReadCeiling(plan *ReadableAllowancePlan, policy ReadableBrokerPolicy, table *jsonx.OrderedMap, path string, origins map[string][]string) {
	if policy.CallBroker != nil {
		return
	}
	hasAlias := policy.ReadCapability != "read" && (slices.Contains(stringList(readableValue(table, "always")), "read") || slices.Contains(stringList(readableValue(table, "on_request")), "read"))
	never := slices.Contains(policy.Never, policy.ReadCapability)
	if !hasAlias && !never {
		return
	}
	plan.NativeReads = append(plan.NativeReads, NativeReadScope{Source: policy.Source, Layer: "ceiling", ReadWord: policy.ReadCapability, Always: hasAlias && slices.Contains(stringList(readableValue(table, "always")), "read"), OnRequest: hasAlias && slices.Contains(stringList(readableValue(table, "on_request")), "read"), Never: never, Origins: map[string][]string{"always": cloneOrigins(origins[path+".always"]), "on_request": cloneOrigins(origins[path+".on_request"]), "never": cloneOrigins(origins[path+".never"])}})
}

func nativeReadRequest(plan *ReadableAllowancePlan, name, line, path string, value any, policy ReadableBrokerPolicy, origins map[string][]string) error {
	if err := nativeReadLegacyAliasRefusal(path, policy, origins); err != nil {
		return err
	}
	if err := validateScopedReadRequest(path, value, policy); err != nil {
		return err
	}
	word, err := requestWord(value)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	plan.NativeReads = append(plan.NativeReads, NativeReadScope{Name: name, Source: policy.Source, Repository: line, Layer: "ask", ReadWord: policy.ReadCapability, Request: word, Origins: map[string][]string{"read": cloneOrigins(origins[path])}})
	return nil
}

func stripScopedNativeRead(policy *ReadableBrokerPolicy, plan *ReadableAllowancePlan, name, line, path string, table *jsonx.OrderedMap, origins map[string][]string) error {
	for _, field := range []string{"always", "on_request", "never"} {
		tokens := stringList(readableValue(table, field))
		if field == "never" || slices.Contains(tokens, policy.ReadCapability) || slices.Contains(tokens, "read") {
			if err := nativeReadLegacyAliasRefusal(path+"."+field, *policy, origins); err != nil {
				return err
			}
		}
	}
	read := NativeReadScope{Name: name, Source: policy.Source, Repository: line, Layer: "ceiling", ReadWord: policy.ReadCapability, Always: slices.Contains(stringList(readableValue(table, "always")), policy.ReadCapability) || slices.Contains(stringList(readableValue(table, "always")), "read"), OnRequest: slices.Contains(stringList(readableValue(table, "on_request")), policy.ReadCapability) || slices.Contains(stringList(readableValue(table, "on_request")), "read"), Never: slices.Contains(stringList(readableValue(table, "never")), policy.ReadCapability) || slices.Contains(stringList(readableValue(table, "never")), "read"), Origins: map[string][]string{"always": cloneOrigins(origins[path+".always"]), "on_request": cloneOrigins(origins[path+".on_request"]), "never": cloneOrigins(origins[path+".never"])}}
	if read.Always || read.OnRequest || read.Never {
		plan.NativeReads = append(plan.NativeReads, read)
	}
	policy.Always = slices.DeleteFunc(policy.Always, func(token string) bool { return token == policy.ReadCapability })
	policy.OnRequest = slices.DeleteFunc(policy.OnRequest, func(token string) bool { return token == policy.ReadCapability })
	return nil
}

func nativeReadLegacyAliasRefusal(path string, policy ReadableBrokerPolicy, origins map[string][]string) error {
	for _, file := range origins[policyListOriginPath(path)] {
		if strings.HasSuffix(file, ".jsonc") || strings.HasSuffix(file, ".json") {
			return fmt.Errorf("%s: literal read for %s is TOML-only; use yolo config migrate, then write %s", path, policy.Source, policy.ReadCapability)
		}
	}
	return nil
}

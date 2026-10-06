package awsauth

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// modellist.go is the BEDROCK LIST FETCH (docs/design/model-lists-and-pickers.md OQ-MM6, ruled
// 2026-10-05): where no selected pack supplies a Bedrock model list, the host reads the region's
// list from Bedrock's control plane itself, with the configured profile's OWN credentials — the
// SSO role's, before any narrowing — so a jail's served credential stays invoke-only and gains no
// list permission. It is the second `aws` surface of this package, beside mint.go's, and like it
// runs `aws` only through the Runner seam, so no test reaches AWS.
//
// # Three invocations, run together
//
// `aws sts get-caller-identity` names the account the list is cached under (the ruling's "per
// account and region"), `aws bedrock list-foundation-models` the region's models with AWS's own
// `providerName` for each, and `aws bedrock list-inference-profiles` the system-defined
// cross-region inference profiles. Each is `--profile <the configured profile> --region
// <region> --output json`, and the CLI follows the pagination itself. They do not depend on one
// another, so they run concurrently and a fetch costs about one CLI start.
//
// # The join
//
// A foundation model whose output modalities include TEXT is kept; every other model (an image
// or embedding model) is not a model an agent can talk to. A kept model's own id is listed when
// AWS says it is callable ON_DEMAND, and every ACTIVE system-defined inference profile backed by
// it is listed under the profile's id (`us.anthropic.…`), because recent Claude models are
// callable on demand only through such a profile. Each entry's maker is the model's
// `providerName`, lowercased: the maker is DECLARED by AWS, never parsed from the id
// (bedrock-plumbing.md OQ-BR9). Entries are sorted by maker, then id, so the order is stable
// from one fetch to the next.
//
// Two more of AWS's own facts ride each entry, for an agent with nothing else to start on to choose
// by (packs/copilot's fetchedStart), since that order puts an old model callable on demand
// (`anthropic.…`) ahead of every cross-region profile (`us.…`): whether AWS marks the model
// LEGACY (its `modelLifecycle`), on its own entry and on every profile it backs, and a profile's
// creation time (`createdAt`, normalized to UTC). An id callable on demand has no date.

// BedrockModel is one entry of a fetched Bedrock list.
type BedrockModel struct {
	// ID is the id an agent sends: a foundation model id, or an inference profile id.
	ID string `json:"id"`
	// Vendor is the model's maker, AWS's providerName lowercased ("anthropic", "openai").
	Vendor string `json:"vendor"`
	// Name is AWS's display name for it: the inference profile's, else the model's.
	Name string `json:"name,omitempty"`
	// Legacy says AWS's modelLifecycle marks the model (the profile's backing model) LEGACY.
	Legacy bool `json:"legacy,omitempty"`
	// Created is an inference profile's creation time as AWS gives it, in RFC 3339 at UTC to the
	// second (createdTime), "" for an id callable on demand or a time that did not parse.
	Created string `json:"created,omitempty"`
}

// ModelList is one region's fetched list, as fetched and as cached.
type ModelList struct {
	Account     string         `json:"account"`
	Region      string         `json:"region"`
	FetchedAtMS int64          `json:"fetched_at_ms"`
	Models      []BedrockModel `json:"models"`
}

// FetchedAt is when the list was fetched.
func (l ModelList) FetchedAt() time.Time { return time.UnixMilli(l.FetchedAtMS) }

// Fresh reports whether the list is younger than ModelListTTL at now.
func (l ModelList) Fresh(now time.Time) bool {
	return l.FetchedAtMS > 0 && now.Sub(l.FetchedAt()) < ModelListTTL
}

// ModelListTTL is how long a fetched list is served before it is fetched again: a day, as ruled.
const ModelListTTL = 24 * time.Hour

// ModelLister fetches a region's list. It holds nothing between calls.
type ModelLister struct {
	Run    Runner
	Binary string
	// Timeout bounds each `aws` invocation; zero is 30 seconds. A fetch is never on a request's
	// path: the launch bounds its own wait and leaves a slower fetch to land in the cache.
	Timeout time.Duration
	Now     func() time.Time
}

func (l ModelLister) binary() string {
	if l.Binary != "" {
		return l.Binary
	}
	return "aws"
}

func (l ModelLister) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l ModelLister) timeout() time.Duration {
	if l.Timeout > 0 {
		return l.Timeout
	}
	return 30 * time.Second
}

// ListArgvs is the three invocations a fetch runs, in the order Fetch reads them: the caller
// identity, the foundation models, the inference profiles.
func (l ModelLister) ListArgvs(profile, region string) [3][]string {
	common := []string{"--profile", profile, "--region", region, "--output", "json"}
	with := func(head ...string) []string {
		return append(append([]string{l.binary()}, head...), common...)
	}
	return [3][]string{
		with("sts", "get-caller-identity"),
		with("bedrock", "list-foundation-models", "--by-output-modality", "TEXT"),
		with("bedrock", "list-inference-profiles", "--type-equals", "SYSTEM_DEFINED"),
	}
}

// Fetch reads region's list for profile. Errors are *MintError, so a caller can say what to fix
// in the words the mint's failures use (a lapsed session names `aws sso login --profile X`).
func (l ModelLister) Fetch(ctx context.Context, profile, region string) (ModelList, error) {
	if l.Run == nil {
		return ModelList{}, &MintError{Kind: FailureUnavailable, Code: "NotConfigured",
			Message: "the aws-auth service has no command runner"}
	}
	if profile == "" {
		return ModelList{}, &MintError{Kind: FailureUnavailable, Code: "NotConfigured",
			Message: "no AWS profile is configured (" + settingsScope(SettingProfile) + ")"}
	}
	if !validRegion(region) {
		return ModelList{}, &MintError{Kind: FailureUnavailable, Code: "InvalidRegion",
			Message: fmt.Sprintf("%q is not an AWS region", region)}
	}
	ctx, cancel := context.WithTimeout(ctx, l.timeout())
	defer cancel()
	argvs := l.ListArgvs(profile, region)
	var outs [3]Output
	var wg sync.WaitGroup
	for i := range argvs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i] = l.Run(ctx, argvs[i])
		}(i)
	}
	wg.Wait()
	for i, out := range outs {
		if err := listFailure(profile, region, argvs[i], out); err != nil {
			return ModelList{}, err
		}
	}
	var identity struct {
		Account string `json:"Account"`
	}
	if err := json.Unmarshal([]byte(outs[0].Stdout), &identity); err != nil || identity.Account == "" {
		return ModelList{}, unparseableList("sts get-caller-identity", err)
	}
	var models foundationModels
	if err := json.Unmarshal([]byte(outs[1].Stdout), &models); err != nil {
		return ModelList{}, unparseableList("bedrock list-foundation-models", err)
	}
	var profiles inferenceProfiles
	if err := json.Unmarshal([]byte(outs[2].Stdout), &profiles); err != nil {
		return ModelList{}, unparseableList("bedrock list-inference-profiles", err)
	}
	return ModelList{
		Account:     identity.Account,
		Region:      region,
		FetchedAtMS: l.now().UnixMilli(),
		Models:      JoinModels(models.ModelSummaries, profiles.InferenceProfileSummaries),
	}, nil
}

// listFailure is one invocation's failure as a *MintError, nil when it succeeded.
func listFailure(profile, region string, argv []string, out Output) error {
	what := strings.Join(argv[1:3], " ")
	if !out.Spawned {
		return &MintError{Kind: FailureCLIMissing, Code: "AwsCliUnavailable",
			Message: "the `aws` CLI could not be run on the host (" + firstLine(out.Stderr) +
				") — install AWS CLI v2"}
	}
	if out.Code == 0 {
		return nil
	}
	err := classify(profile, out)
	switch err.Kind {
	case FailureLoginRequired, FailureProfileMissing:
		return err
	}
	// AWS's own words, naming the call it refused: a role that may invoke but not list says
	// so here, and nothing in this package knows IAM's vocabulary better than AWS does.
	return &MintError{Kind: FailureRejected, Code: "ListFailed",
		Message: "`aws " + what + "` in " + region + " as profile " + profile + " failed: " +
			firstLine(out.Stderr)}
}

func unparseableList(what string, err error) error {
	detail := "it named no account"
	if err != nil {
		detail = err.Error()
	}
	return &MintError{Kind: FailureUnavailable, Code: "UnparseableAwsOutput",
		Message: "`aws " + what + "` returned output this service could not parse: " + detail}
}

// validRegion is one DNS label of lowercase letters, digits and hyphens: what an `--region`
// argument may be, so nothing a caller sends can become a second CLI flag.
func validRegion(region string) bool {
	if region == "" || len(region) > 63 || region[0] == '-' {
		return false
	}
	for _, r := range region {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// foundationModels is `aws bedrock list-foundation-models --output json`.
type foundationModels struct {
	ModelSummaries []FoundationModel `json:"modelSummaries"`
}

// FoundationModel is the part of one model summary the join reads.
type FoundationModel struct {
	ModelArn                string   `json:"modelArn"`
	ModelID                 string   `json:"modelId"`
	ModelName               string   `json:"modelName"`
	ProviderName            string   `json:"providerName"`
	OutputModalities        []string `json:"outputModalities"`
	InferenceTypesSupported []string `json:"inferenceTypesSupported"`
	ModelLifecycle          struct {
		// Status is ACTIVE or LEGACY.
		Status string `json:"status"`
	} `json:"modelLifecycle"`
}

// legacy reports whether AWS marks m LEGACY.
func (m FoundationModel) legacy() bool {
	return strings.EqualFold(strings.TrimSpace(m.ModelLifecycle.Status), "LEGACY")
}

// inferenceProfiles is `aws bedrock list-inference-profiles --output json`.
type inferenceProfiles struct {
	InferenceProfileSummaries []InferenceProfile `json:"inferenceProfileSummaries"`
}

// InferenceProfile is the part of one inference profile summary the join reads.
type InferenceProfile struct {
	InferenceProfileID   string `json:"inferenceProfileId"`
	InferenceProfileName string `json:"inferenceProfileName"`
	Status               string `json:"status"`
	Type                 string `json:"type"`
	// CreatedAt is when AWS created the profile, as the CLI prints a timestamp (ISO 8601).
	CreatedAt string `json:"createdAt"`
	Models    []struct {
		ModelArn string `json:"modelArn"`
	} `json:"models"`
}

// JoinModels is the file comment's join: every text-output model callable on demand under its
// own id, and every active system-defined inference profile backed by a text-output model, each
// with the model's maker, sorted by maker and then id, one entry per id.
func JoinModels(models []FoundationModel, profiles []InferenceProfile) []BedrockModel {
	text := map[string]FoundationModel{} // by model id and by model ARN
	var out []BedrockModel
	seen := map[string]bool{}
	add := func(m BedrockModel) {
		if m.ID == "" || m.Vendor == "" || seen[m.ID] {
			return
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	for _, m := range models {
		if m.ModelID == "" || !hasFold(m.OutputModalities, "TEXT") {
			continue
		}
		text[m.ModelID] = m
		if m.ModelArn != "" {
			text[m.ModelArn] = m
		}
		if hasFold(m.InferenceTypesSupported, "ON_DEMAND") {
			add(BedrockModel{ID: m.ModelID, Vendor: vendorOf(m), Name: m.ModelName, Legacy: m.legacy()})
		}
	}
	for _, p := range profiles {
		if p.InferenceProfileID == "" || (p.Type != "" && !strings.EqualFold(p.Type, "SYSTEM_DEFINED")) ||
			(p.Status != "" && !strings.EqualFold(p.Status, "ACTIVE")) {
			continue
		}
		for _, backing := range p.Models {
			m, ok := text[backing.ModelArn]
			if !ok {
				m, ok = text[foundationModelID(backing.ModelArn)]
			}
			if !ok {
				continue
			}
			name := p.InferenceProfileName
			if name == "" {
				name = m.ModelName
			}
			add(BedrockModel{ID: p.InferenceProfileID, Vendor: vendorOf(m), Name: name, Legacy: m.legacy(),
				Created: createdTime(p.CreatedAt)})
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Vendor != out[j].Vendor {
			return out[i].Vendor < out[j].Vendor
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// createdTime is an AWS timestamp in RFC 3339 at UTC to the second, the one spelling a reader may
// compare as a string; "" for one that does not parse.
func createdTime(raw string) string {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// vendorOf is a model's maker as the list declares it: AWS's providerName, lowercased.
func vendorOf(m FoundationModel) string {
	return strings.ToLower(strings.TrimSpace(m.ProviderName))
}

// foundationModelID is the model id an inference profile's backing-model ARN names: the resource
// after `foundation-model/`, the ARN shape AWS documents for a foundation model. "" for any other
// ARN.
func foundationModelID(arn string) string {
	const marker = ":foundation-model/"
	i := strings.LastIndex(arn, marker)
	if i < 0 {
		return ""
	}
	return arn[i+len(marker):]
}

func hasFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

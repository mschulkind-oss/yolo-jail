package packload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// PrivateProgramEnvPlan is nonsecret metadata for one selected client contribution.
type PrivateProgramEnvPlan struct {
	Bin, Pack, Front, RecipePack, RecipeIdentity string
	TokenVariable, URLVariable, TrustVariable    string
}

// ResolvePrivateProgramEnv resolves env.bin declarations only against the selected pack set and
// the caller-supplied, already selected and approved HTTP front. It never resolves values.
func ResolvePrivateProgramEnv(packs []*Pack, front, frontPack string, enabledAndApproved bool) ([]PrivateProgramEnvPlan, error) {
	if !enabledAndApproved || front == "" || frontPack == "" {
		return nil, errors.New("private client env has no selected approved front")
	}
	type owner struct {
		name string
		decl *packdecl.Manifest
	}
	var owners []owner
	for _, p := range packs {
		if p != nil && p.Decl != nil {
			owners = append(owners, owner{name: p.Name, decl: p.Decl})
		}
	}
	var plans []PrivateProgramEnvPlan
	seenBins := map[string]bool{}
	for _, owner := range owners {
		for _, env := range owner.decl.PrivateProgramEnvContributions() {
			if env.ServedBy != front {
				continue
			}
			if seenBins[env.Bin] {
				return nil, fmt.Errorf("selected private env has multiple recipes for client %q", env.Bin)
			}
			seenBins[env.Bin] = true
			plan := PrivateProgramEnvPlan{Bin: env.Bin, Pack: owner.name, Front: front}
			for key, value := range env.Vars {
				if strings.Contains(value, "{caller_token}") {
					if plan.TokenVariable != "" {
						return nil, fmt.Errorf("private client %q declares multiple token variables", env.Bin)
					}
					plan.TokenVariable = key
				}
				if strings.Contains(value, "{listen}") {
					if plan.URLVariable != "" {
						return nil, fmt.Errorf("private client %q declares multiple URL variables", env.Bin)
					}
					plan.URLVariable = key
				}
				if strings.Contains(value, "{client_trust}") {
					if plan.TrustVariable != "" {
						return nil, fmt.Errorf("private client %q declares multiple trust variables", env.Bin)
					}
					plan.TrustVariable = key
				}
			}
			if plan.TokenVariable == "" || plan.URLVariable == "" || plan.TrustVariable == "" {
				return nil, fmt.Errorf("private client %q needs token, URL and trust variable declarations", env.Bin)
			}
			if plan.TokenVariable == plan.URLVariable || plan.TokenVariable == plan.TrustVariable || plan.URLVariable == plan.TrustVariable {
				return nil, fmt.Errorf("private client %q reuses a managed variable name", env.Bin)
			}
			var recipes []struct {
				pack string
				inst packdecl.Install
			}
			for _, candidate := range owners {
				for _, inst := range candidate.decl.InstallContributions() {
					if inst.Bin == env.Bin {
						recipes = append(recipes, struct {
							pack string
							inst packdecl.Install
						}{candidate.name, inst})
					}
				}
			}
			if len(recipes) == 1 {
				identity, err := ProgramRecipeIdentity(recipes[0].inst)
				if err != nil {
					return nil, err
				}
				plan.RecipePack = recipes[0].pack
				plan.RecipeIdentity = identity
			} else if len(recipes) > 1 {
				return nil, fmt.Errorf("selected private client %q has colliding program recipes", env.Bin)
			} else {
				var needers []string
				for _, candidate := range owners {
					for _, need := range candidate.decl.RequiredBins() {
						if need.Bin == env.Bin && need.Need {
							needers = append(needers, candidate.name)
						}
					}
				}
				if len(needers) != 1 {
					return nil, fmt.Errorf("selected private client %q has no unique selected recipe or asserted need", env.Bin)
				}
				plan.RecipePack = needers[0]
				plan.RecipeIdentity = "need:" + needers[0] + ":" + env.Bin
			}
			plans = append(plans, plan)
		}
	}
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].Pack != plans[j].Pack {
			return plans[i].Pack < plans[j].Pack
		}
		return plans[i].Bin < plans[j].Bin
	})
	return plans, nil
}

// ProgramRecipeIdentity is a selected `program` contribution's recipe identity, the one a
// private client plan carries (PrivateProgramEnvPlan.RecipeIdentity) and its producer checks.
func ProgramRecipeIdentity(inst packdecl.Install) (string, error) {
	encoded, err := json.Marshal(inst)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

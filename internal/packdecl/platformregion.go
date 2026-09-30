package packdecl

import "fmt"

// PlatformRegion is which of a platform's region variables one PROGRAM reads
// (Contribution.PlatformRegions): on Platform, this program takes its region from RegionEnvName
// and from nothing else the platform's providers list.
type PlatformRegion struct {
	// Platform is the provider platform the list is about, "aws-bedrock" for opencode's.
	Platform string `json:"platform"`
	// RegionEnvName is the variables this program reads its region from on that platform, in
	// the order it reads them. It replaces, for this program alone, the list a provider of the
	// platform declares under its own `region_env_name`.
	RegionEnvName []string `json:"region_env_name"`
}

// RegionEnvNamesFor is the region variables the program installing bin reads on platform, nil
// when it declares none for that platform (it reads every variable the platform's providers
// list) or the manifest installs no such program.
func (m *Manifest) RegionEnvNamesFor(bin, platform string) []string {
	for _, c := range m.Contributions() {
		if c.Kind != KindProgram || c.Bin != bin {
			continue
		}
		for _, r := range c.PlatformRegions {
			if r.Platform == platform {
				return r.RegionEnvName
			}
		}
		return nil
	}
	return nil
}

// platformRegionProblems validates a program's platform_regions: each entry needs a platform of
// the platform shape, listed once, and a non-empty list of usable variable names. On any kind
// but program the list is refused, as `region_env_name` is on any kind but provider: a
// declaration no consumer reads is the accepted-and-ignored shape this schema refuses everywhere.
func platformRegionProblems(label string, c Contribution) []string {
	var out []string
	if len(c.RegionEnvName) > 0 && c.Kind != KindProvider {
		out = append(out, fmt.Sprintf("%s: kind %q does not take \"region_env_name\" — it names the "+
			"variables a PROVIDER's platform reads its region from; a program that reads fewer "+
			"of them says so under \"platform_regions\"", label, c.Kind))
	}
	if len(c.PlatformRegions) == 0 {
		return out
	}
	if c.Kind != KindProgram {
		return append(out, fmt.Sprintf("%s: kind %q does not take \"platform_regions\" — it says "+
			"which region variables a PROGRAM reads, so only \"program\" has one", label, c.Kind))
	}
	seen := map[string]bool{}
	for i, r := range c.PlatformRegions {
		at := fmt.Sprintf("%s.platform_regions[%d]", label, i)
		switch {
		case r.Platform == "":
			out = append(out, at+": needs the \"platform\" whose region variables it narrows")
		case PlatformProblem(at+".platform", r.Platform) != "":
			out = append(out, PlatformProblem(at+".platform", r.Platform))
		case seen[r.Platform]:
			out = append(out, fmt.Sprintf("%s.platform: %q is listed twice", at, r.Platform))
		}
		seen[r.Platform] = true
		if len(r.RegionEnvName) == 0 {
			out = append(out, at+".region_env_name: names no variable, so the program could "+
				"never be given a region by one — list the variables it reads")
			continue
		}
		out = append(out, regionEnvNameProblems(at, r.RegionEnvName)...)
	}
	return out
}

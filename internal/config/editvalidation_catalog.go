package config

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/modelcatalog"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

type explicitOfflineCatalog struct {
	bin   string
	globs []string
	dir   string
}

func explicitProviderCatalog(ctx *editValidationContext, config *jsonx.OrderedMap) (map[string]map[string]packdecl.ProviderModel, error) {
	catalog := map[string]map[string]packdecl.ProviderModel{}
	authored, _ := config.Get(providersKey)
	authoredProviders, _ := asMap(authored)
	declarations, err := packload.ComposeProviderDeclarations(authoredProviders, ctx.packs)
	if err != nil {
		return nil, fmt.Errorf("compose provider declarations for offline catalog: %w", err)
	}
	factsByID, err := offlineCatalogFacts(ctx.rawUser, ctx.packs)
	if err != nil {
		return nil, err
	}
	for _, declaration := range declarations {
		if len(factsByID) == 0 {
			break
		}
		providerFacts := make(map[string]packdecl.ProviderModel, len(factsByID))
		for id, facts := range factsByID {
			providerFacts[id] = facts
		}
		catalog[declaration.Name] = providerFacts
	}
	return catalog, nil
}

// WithOfflineModelCatalog is the launch's catalog for the readable model rows
// (packload.WithModelCatalog; docs/reference/config/user-config.md#lines-you-never-write C7, docs/reference/config/rulings.md
// RC-D29): the same offline facts an edit's validation fills a row from, read from the host floor's
// provisioned packages, so a model named without facts launches with the facts `yolo config set`
// checked it against. user is the config the host floor and provisioner keys are read from. The
// read runs only when the composed table holds a readable row; a host floor with no catalog leaves
// the rows with what they wrote.
func WithOfflineModelCatalog(user *jsonx.OrderedMap, packs []*packload.Pack) packload.ComposeOption {
	return packload.WithModelCatalog(func() (map[string]packdecl.ProviderModel, error) {
		if user == nil {
			user = jsonx.NewOrderedMap()
		}
		return offlineCatalogFacts(user, packs)
	})
}

// offlineCatalogFacts reads every selected pack's offline model catalog from the host floor's
// provisioned npm packages: the facts by model id, the first package to name an id winning.
func offlineCatalogFacts(rawUser *jsonx.OrderedMap, packs []*packload.Pack) (map[string]packdecl.ProviderModel, error) {
	var packPrograms []hostfloor.PackPrograms
	for _, pack := range packs {
		installs, _ := pack.HonoredInstalls()
		packPrograms = append(packPrograms, hostfloor.PackPrograms{Pack: pack.Name, Installs: installs})
	}
	programs := hostfloor.Programs(packPrograms)
	programByBin := map[string]hostfloor.Program{}
	for _, program := range programs {
		programByBin[program.Bin()] = program
	}
	wire := hostFloorWire(rawUser)
	floor := &hostfloor.Floor{
		Dir: paths.HostFloorDir(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		NodeFloor: hostfloor.HighestNodeFloor(programs),
		Include: func(pack string) bool {
			allowed, _ := PackPolicyDecision(wire, pack)
			return allowed
		},
		Outranked: hostfloor.Outranker(func(bin string) []string {
			return provisionerOrder(rawUser, ProvisionerEnvHost, bin)
		}, func(bin string) (string, error) { return exec.LookPath(bin) }),
	}

	var sources []explicitOfflineCatalog
	for _, pack := range packs {
		installs, _ := pack.HonoredInstalls()
		for _, install := range installs {
			if install.Kind != "npm" || len(install.ModelCatalog) == 0 {
				continue
			}
			source := explicitOfflineCatalog{bin: install.Bin, globs: append([]string(nil), install.ModelCatalog...)}
			program, ok := programByBin[install.Bin]
			if !ok {
				sources = append(sources, source)
				continue
			}
			status := floor.Status(program)
			if status.Disposition != hostfloor.Provisioned || status.Record == nil {
				sources = append(sources, source)
				continue
			}
			source.dir = status.Record.NpmPackageDir(program.Install.Package)
			sources = append(sources, source)
		}
	}

	factsByID := map[string]packdecl.ProviderModel{}
	for _, source := range sources {
		if source.dir == "" {
			continue
		}
		if !modelcatalog.Installed(source.dir) {
			continue
		}
		read, err := modelcatalog.ReadCanonicalFacts(source.dir, source.globs)
		if err != nil {
			return nil, fmt.Errorf("offline provider model catalog for %s (%s): %w; repair the installed package or re-provision it with `yolo host -- %s`", source.bin, source.dir, err, source.bin)
		}
		if read.Files == 0 {
			continue
		}
		for id, facts := range read.Facts {
			if _, exists := factsByID[id]; !exists {
				factsByID[id] = facts
			}
		}
	}

	return factsByID, nil
}

package hostskills

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

// The plugin copy is one effect boundary, injectable for preservation failures.
var copyPluginTree = copyTree

func readPluginSignIn(dir, format string) (mcpsignin.Inventory, error) {
	reader, err := mcpsignin.OpenDisk(dir)
	if err != nil {
		return mcpsignin.Inventory{}, err
	}
	defer reader.Close()
	inventory := mcpsignin.ReadInventory(reader, ".", format)
	if len(inventory.Problems) > 0 {
		return inventory, fmt.Errorf("%s", strings.Join(inventory.Problems, "; "))
	}
	return inventory, nil
}
func signInDeliveryRefused(name, dest string, err error) []Result {
	return []Result{{Name: name, Path: dest, Action: ActionRefused, Detail: "sign-in bundle not delivered: " + err.Error() + "; restore the retained original, then relaunch"}}
}

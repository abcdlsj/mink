package host

import (
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/computer/enginefactory"
)

func CapabilityInventory() (*computerv1.CapabilityInventoryDeclaration, error) {
	return enginefactory.Discover().Inventory(nil)
}

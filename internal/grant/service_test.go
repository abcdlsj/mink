package grant

import (
	"testing"

	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/internal/store"
)

func TestComputerPairCapabilityMapping(t *testing.T) {
	name, ok := capabilityName(grantv1.Capability_CAPABILITY_COMPUTER_PAIR)
	if !ok || name != store.CapabilityComputerPair {
		t.Fatalf("computer pair capability name = %q, %v", name, ok)
	}
	if value := capabilityValue(store.CapabilityComputerPair); value != grantv1.Capability_CAPABILITY_COMPUTER_PAIR {
		t.Fatalf("computer pair capability value = %v", value)
	}
}

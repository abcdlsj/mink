package computer

import (
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/store"
)

func TestComputerOnlineProjectionUsesIndependentConnectivityLease(t *testing.T) {
	lastSeenAt := time.Unix(1_800_000_000, 0).UTC()
	computer := store.Computer{LastSeenAt: lastSeenAt}
	online := computerMessage(computer, lastSeenAt.Add(connectivityTTL-time.Nanosecond))
	if !online.GetOnline() || !online.GetConnectivityExpiresAt().AsTime().Equal(lastSeenAt.Add(connectivityTTL)) {
		t.Fatalf("online projection = %v", online)
	}
	offline := computerMessage(computer, lastSeenAt.Add(connectivityTTL))
	if offline.GetOnline() {
		t.Fatalf("computer remained online at expiry: %v", offline)
	}
}

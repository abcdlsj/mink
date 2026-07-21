package audit

import (
	"testing"

	auditv1 "github.com/abcdlsj/sumi/gen/go/sumi/audit/v1"
	"github.com/abcdlsj/sumi/internal/store"
)

func TestComputerPairAuditMapping(t *testing.T) {
	if value := actionValue(store.AuditComputerPairPrepare); value != auditv1.AuditAction_AUDIT_ACTION_COMPUTER_PAIR_PREPARE {
		t.Fatalf("pair prepare action = %v", value)
	}
	if value := actionValue(store.AuditComputerPair); value != auditv1.AuditAction_AUDIT_ACTION_COMPUTER_PAIR {
		t.Fatalf("pair action = %v", value)
	}
	if value := targetValue("computer_pairing"); value != auditv1.AuditTargetKind_AUDIT_TARGET_KIND_COMPUTER_PAIRING {
		t.Fatalf("computer pairing target = %v", value)
	}
	if value := targetValue("computer"); value != auditv1.AuditTargetKind_AUDIT_TARGET_KIND_COMPUTER {
		t.Fatalf("computer target = %v", value)
	}
}

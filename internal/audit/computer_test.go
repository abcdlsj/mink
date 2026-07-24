package audit

import (
	"testing"

	auditv1 "github.com/abcdlsj/sumi/gen/go/sumi/audit/v1"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/store"
)

func TestComputerPairAuditMapping(t *testing.T) {
	if v := servicesvc.AuditActionToProto(store.AuditComputerPairPrepare); v != auditv1.AuditAction_AUDIT_ACTION_COMPUTER_PAIR_PREPARE {
		t.Fatalf("pair prepare action = %v", v)
	}
	if v := servicesvc.AuditActionToProto(store.AuditComputerPair); v != auditv1.AuditAction_AUDIT_ACTION_COMPUTER_PAIR {
		t.Fatalf("pair action = %v", v)
	}
	if v := servicesvc.AuditTargetToProto("computer_pairing"); v != auditv1.AuditTargetKind_AUDIT_TARGET_KIND_COMPUTER_PAIRING {
		t.Fatalf("computer pairing target = %v", v)
	}
	if v := servicesvc.AuditTargetToProto("computer"); v != auditv1.AuditTargetKind_AUDIT_TARGET_KIND_COMPUTER {
		t.Fatalf("computer target = %v", v)
	}
}

package audit

import (
	"testing"
	"time"

	auditv1 "github.com/abcdlsj/sumi/gen/go/sumi/audit/v1"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestEventMessagePreservesAgentTargetAndSpaceContext(t *testing.T) {
	spaceID := uuid.NewString()
	agentID := uuid.NewString()
	event := eventToProto(store.AuditEvent{
		Sequence:       9,
		ID:             uuid.NewString(),
		OrganizationID: uuid.NewString(),
		Actor:          store.Principal{Kind: "human", ID: uuid.NewString()},
		Action:         store.AuditSpaceMemberRemove,
		TargetKind:     "agent",
		TargetID:       agentID,
		ContextKind:    "space",
		ContextID:      spaceID,
		Outcome:        "committed",
		OccurredAt:     time.Now(),
	})
	if event.GetContextKind() != auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_SPACE || event.GetContextId() != spaceID {
		t.Fatalf("audit API context = %v/%q", event.GetContextKind(), event.GetContextId())
	}
	if event.GetTargetKind() != auditv1.AuditTargetKind_AUDIT_TARGET_KIND_AGENT || event.GetTargetId() != agentID {
		t.Fatalf("audit API target = %v/%q", event.GetTargetKind(), event.GetTargetId())
	}
}

package messagecodec

import (
	"testing"
	"time"

	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestMessageAndHeldDraftRejectInvalidStoreFacts(t *testing.T) {
	spaceID := uuid.NewString()
	message := store.Message{
		ID: uuid.NewString(), RequestID: uuid.NewString(), SpaceID: spaceID,
		Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: spaceID},
		Author: store.Principal{Kind: "agent", ID: uuid.NewString()}, Body: "result",
	}
	if _, err := Message(message); err != nil {
		t.Fatalf("valid message error = %v", err)
	}
	message.Target.ID = uuid.NewString()
	if _, err := Message(message); err == nil {
		t.Fatal("space target mismatch accepted")
	}

	draft := store.HeldDraft{
		ID: uuid.NewString(), InboxItemID: uuid.NewString(), SpaceID: spaceID,
		Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: spaceID},
		Body:   "held", State: store.HeldDraftStateHeld, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if _, err := HeldDraft(draft); err != nil {
		t.Fatalf("valid held draft error = %v", err)
	}
	draft.ResolutionAction = store.DraftResolutionCancel
	if _, err := HeldDraft(draft); err == nil {
		t.Fatal("held draft with resolution accepted")
	}
}

func TestInputValidationAndTargetMapping(t *testing.T) {
	id := uuid.NewString()
	target, err := ParseTarget(&spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: id}})
	if err != nil || target.Kind != store.MessageTargetSpace || target.ID != id {
		t.Fatalf("parse target = %+v, %v", target, err)
	}
	if _, err := MentionedPrincipals([]*spacev1.Principal{
		{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: id},
		{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: id},
	}); err == nil {
		t.Fatal("duplicate mentions accepted")
	}
	if err := ValidateBody(""); err == nil {
		t.Fatal("empty body accepted")
	}
}

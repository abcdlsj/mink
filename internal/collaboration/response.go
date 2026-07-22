package collaboration

import (
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func principalMessage(principal authoritydomain.Principal) *spacev1.Principal {
	return &spacev1.Principal{Kind: principalKind(principal.Kind), Id: principal.ID}
}

func spaceMessage(space collaborationapp.Space) *spacev1.Space {
	kind := spacev1.SpaceKind_SPACE_KIND_UNSPECIFIED
	if space.Kind == collaborationdomain.SpaceDM {
		kind = spacev1.SpaceKind_SPACE_KIND_DM
	} else if space.Kind == collaborationdomain.SpaceGroup {
		kind = spacev1.SpaceKind_SPACE_KIND_GROUP
	}
	message := &spacev1.Space{
		Id: space.ID, OrganizationId: space.OrganizationID, Kind: kind, Name: space.Name,
		CreatedAt: timestamppb.New(space.CreatedAt), UpdatedAt: timestamppb.New(space.UpdatedAt),
	}
	if space.ArchivedAt != nil {
		message.ArchivedAt = timestamppb.New(*space.ArchivedAt)
	}
	return message
}

func membershipMessage(membership collaborationapp.Membership) *spacev1.Membership {
	return &spacev1.Membership{
		SpaceId: membership.SpaceID, Principal: principalMessage(membership.Principal), JoinedAt: timestamppb.New(membership.JoinedAt),
	}
}

func threadMessage(thread collaborationapp.Thread) *spacev1.Thread {
	return &spacev1.Thread{Id: thread.ID, SpaceId: thread.SpaceID, CreatedAt: timestamppb.New(thread.CreatedAt)}
}

func messageMessage(message collaborationapp.Message) *spacev1.Message {
	result := &spacev1.Message{
		Id: message.ID, RequestId: message.RequestID, SpaceId: message.SpaceID,
		TargetSequence: message.TargetSequence, Author: principalMessage(message.Author), Body: message.Body,
		CreatedAt: timestamppb.New(message.CreatedAt), MentionedAgentIds: message.MentionedAgentIDs,
	}
	if message.Target.Kind == collaborationdomain.TargetThread {
		result.ThreadRootMessageId = message.Target.ID
	}
	return result
}

func receiptSnapshotMessage(receipt ReceiptSnapshot) *spacev1.MutationReceipt {
	return &spacev1.MutationReceipt{RequestId: receipt.RequestID, CommittedAt: timestamppb.New(receipt.CommittedAt)}
}

func principalKind(kind authoritydomain.PrincipalKind) spacev1.PrincipalKind {
	if kind == authoritydomain.PrincipalHuman {
		return spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	}
	if kind == authoritydomain.PrincipalAgent {
		return spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT
	}
	return spacev1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
}

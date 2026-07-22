package collaboration

import (
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func principalMessage(principal store.Principal) *spacev1.Principal {
	return &spacev1.Principal{Kind: principalKind(collaborationdomain.PrincipalKind(principal.Kind)), Id: principal.ID}
}

func spaceMessage(space store.Space) *spacev1.Space {
	kind := spacev1.SpaceKind_SPACE_KIND_UNSPECIFIED
	if collaborationdomain.SpaceKind(space.Kind) == collaborationdomain.SpaceDM {
		kind = spacev1.SpaceKind_SPACE_KIND_DM
	} else if collaborationdomain.SpaceKind(space.Kind) == collaborationdomain.SpaceGroup {
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

func spaceSnapshotMessage(space SpaceSnapshot) *spacev1.Space {
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

func membershipMessage(membership store.Membership) *spacev1.Membership {
	return &spacev1.Membership{
		SpaceId: membership.SpaceID, Principal: principalMessage(membership.Principal), JoinedAt: timestamppb.New(membership.JoinedAt),
	}
}

func threadMessage(thread store.Thread) *spacev1.Thread {
	return &spacev1.Thread{Id: thread.ID, SpaceId: thread.SpaceID, CreatedAt: timestamppb.New(thread.CreatedAt)}
}

func messageMessage(message store.Message) *spacev1.Message {
	result := &spacev1.Message{
		Id: message.ID, RequestId: message.RequestID, SpaceId: message.SpaceID,
		TargetSequence: message.TargetSequence, Author: principalMessage(message.Author), Body: message.Body,
		CreatedAt: timestamppb.New(message.CreatedAt), MentionedAgentIds: message.MentionedAgentIDs,
	}
	if message.Target.Kind == store.MessageTargetThread {
		result.ThreadRootMessageId = message.Target.ID
	}
	return result
}

func messageSnapshotMessage(message MessageSnapshot) *spacev1.Message {
	result := &spacev1.Message{
		Id: message.ID, RequestId: message.RequestID, SpaceId: message.SpaceID,
		TargetSequence: message.TargetSequence,
		Author:         &spacev1.Principal{Kind: principalKind(message.Author.Kind), Id: message.Author.ID},
		Body:           message.Body, MentionedAgentIds: message.MentionedAgentIDs, CreatedAt: timestamppb.New(message.CreatedAt),
	}
	if message.Target.Kind == TargetThread {
		result.ThreadRootMessageId = message.Target.ID
	}
	return result
}

func receiptSnapshotMessage(receipt ReceiptSnapshot) *spacev1.MutationReceipt {
	return &spacev1.MutationReceipt{RequestId: receipt.RequestID, CommittedAt: timestamppb.New(receipt.CommittedAt)}
}

func principalKind(kind collaborationdomain.PrincipalKind) spacev1.PrincipalKind {
	if kind == collaborationdomain.PrincipalHuman {
		return spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	}
	if kind == collaborationdomain.PrincipalAgent {
		return spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT
	}
	return spacev1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
}

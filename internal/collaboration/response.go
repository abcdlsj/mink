package collaboration

import (
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
	"github.com/abcdlsj/sumi/internal/servicesvc"
)

var kindToProto = map[collaborationdomain.SpaceKind]spacev1.SpaceKind{
	collaborationdomain.SpaceDM:    spacev1.SpaceKind_SPACE_KIND_DM,
	collaborationdomain.SpaceGroup: spacev1.SpaceKind_SPACE_KIND_GROUP,
}

var kindToDomain = map[spacev1.PrincipalKind]authoritydomain.PrincipalKind{
	spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN: authoritydomain.PrincipalHuman,
	spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT: authoritydomain.PrincipalAgent,
}

func principalToSpaceProto(p authoritydomain.Principal) *spacev1.Principal {
	return &spacev1.Principal{Kind: principalKindToProto(p.Kind), Id: p.ID}
}

func spaceToProto(s collaborationapp.Space) *spacev1.Space {
	msg := &spacev1.Space{
		Id: s.ID, OrganizationId: s.OrganizationID,
		Kind:      kindToProto[s.Kind],
		Name:      s.Name,
		CreatedAt: servicesvc.Ts(s.CreatedAt),
		UpdatedAt: servicesvc.Ts(s.UpdatedAt),
	}
	if s.ArchivedAt != nil {
		msg.ArchivedAt = servicesvc.Ts(*s.ArchivedAt)
	}
	return msg
}

func membershipToProto(m collaborationapp.Membership) *spacev1.Membership {
	return &spacev1.Membership{
		SpaceId:   m.SpaceID,
		Principal: principalToSpaceProto(m.Principal),
		JoinedAt:  servicesvc.Ts(m.JoinedAt),
	}
}

func threadToProto(t collaborationapp.Thread) *spacev1.Thread {
	return &spacev1.Thread{
		Id: t.ID, SpaceId: t.SpaceID, CreatedAt: servicesvc.Ts(t.CreatedAt),
	}
}

func msgToProto(m collaborationapp.Message) *spacev1.Message {
	msg := &spacev1.Message{
		Id: m.ID, RequestId: m.RequestID, SpaceId: m.SpaceID,
		TargetSequence: m.TargetSequence, Author: principalToSpaceProto(m.Author),
		Body: m.Body, CreatedAt: servicesvc.Ts(m.CreatedAt),
		MentionedPrincipals: principalsToProto(m.MentionedPrincipals),
	}
	if m.Target.Kind == collaborationdomain.TargetThread {
		msg.ThreadRootMessageId = m.Target.ID
	}
	return msg
}

func principalsToProto(principals []authoritydomain.Principal) []*spacev1.Principal {
	result := make([]*spacev1.Principal, 0, len(principals))
	for _, p := range principals {
		result = append(result, principalToSpaceProto(p))
	}
	return result
}

func receiptToProto(r ReceiptSnapshot) *spacev1.MutationReceipt {
	return &spacev1.MutationReceipt{
		RequestId: r.RequestID, CommittedAt: servicesvc.Ts(r.CommittedAt),
	}
}

func principalKindToProto(kind authoritydomain.PrincipalKind) spacev1.PrincipalKind {
	switch kind {
	case authoritydomain.PrincipalHuman:
		return spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	case authoritydomain.PrincipalAgent:
		return spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT
	default:
		return spacev1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	}
}

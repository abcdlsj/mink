package collaboration

import (
	"context"
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
	"github.com/abcdlsj/sumi/internal/store"
)

type PrincipalRef struct {
	Kind           collaborationdomain.PrincipalKind
	ID             string
	OrganizationID string
}

type MessageTargetRef struct {
	Kind collaborationdomain.MessageTargetKind
	ID   string
}

const (
	TargetSpace  = collaborationdomain.TargetSpace
	TargetThread = collaborationdomain.TargetThread
)

type SpaceSnapshot struct {
	ID             string
	OrganizationID string
	Kind           collaborationdomain.SpaceKind
	Name           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ArchivedAt     *time.Time
}

type MessageSnapshot struct {
	ID                string
	RequestID         string
	SpaceID           string
	Target            MessageTargetRef
	TargetSequence    uint64
	Author            PrincipalRef
	Body              string
	MentionedAgentIDs []string
	CreatedAt         time.Time
}

type ReceiptSnapshot struct {
	RequestID   string
	CommittedAt time.Time
}

type CreateDMCommand struct {
	RequestID string
	Actor     PrincipalRef
	Peer      PrincipalRef
	Now       time.Time
}

type CreateGroupCommand struct {
	RequestID string
	Actor     PrincipalRef
	Name      string
	Now       time.Time
}

type ChangeMemberCommand struct {
	RequestID string
	Actor     PrincipalRef
	SpaceID   string
	Member    PrincipalRef
	Now       time.Time
}

type ChangeSpaceArchiveCommand struct {
	RequestID string
	Actor     PrincipalRef
	SpaceID   string
	Now       time.Time
}

type SendMessageCommand struct {
	RequestID         string
	Actor             PrincipalRef
	Target            MessageTargetRef
	Body              string
	MentionedAgentIDs []string
	Now               time.Time
}

func (s *Service) createDM(ctx context.Context, command CreateDMCommand) (SpaceSnapshot, error) {
	space, err := s.store.CreateDM(ctx, store.CreateDMParams{
		RequestID: command.RequestID,
		Actor:     command.Actor.storePrincipal(),
		Peer:      command.Peer.storePrincipal(),
		Now:       command.Now,
	})
	if err != nil {
		return SpaceSnapshot{}, err
	}
	return newSpaceSnapshot(space), nil
}

func (s *Service) createGroup(ctx context.Context, command CreateGroupCommand) (SpaceSnapshot, error) {
	space, err := s.store.CreateGroup(ctx, store.CreateGroupParams{
		RequestID: command.RequestID,
		Actor:     command.Actor.storePrincipal(),
		Name:      command.Name,
		Now:       command.Now,
	})
	if err != nil {
		return SpaceSnapshot{}, err
	}
	return newSpaceSnapshot(space), nil
}

func (s *Service) addMember(ctx context.Context, command ChangeMemberCommand) (ReceiptSnapshot, error) {
	receipt, err := s.store.AddMember(ctx, command.storeParams())
	if err != nil {
		return ReceiptSnapshot{}, err
	}
	return newReceiptSnapshot(receipt), nil
}

func (s *Service) removeMember(ctx context.Context, command ChangeMemberCommand) (ReceiptSnapshot, error) {
	receipt, err := s.store.RemoveMember(ctx, command.storeParams())
	if err != nil {
		return ReceiptSnapshot{}, err
	}
	return newReceiptSnapshot(receipt), nil
}

func (s *Service) archiveSpace(ctx context.Context, command ChangeSpaceArchiveCommand) (ReceiptSnapshot, error) {
	receipt, err := s.store.ArchiveSpace(ctx, command.storeParams())
	if err != nil {
		return ReceiptSnapshot{}, err
	}
	return newReceiptSnapshot(receipt), nil
}

func (s *Service) unarchiveSpace(ctx context.Context, command ChangeSpaceArchiveCommand) (ReceiptSnapshot, error) {
	receipt, err := s.store.UnarchiveSpace(ctx, command.storeParams())
	if err != nil {
		return ReceiptSnapshot{}, err
	}
	return newReceiptSnapshot(receipt), nil
}

func (s *Service) sendMessage(ctx context.Context, command SendMessageCommand) (MessageSnapshot, error) {
	message, err := s.store.SendMessage(ctx, store.SendMessageParams{
		RequestID:         command.RequestID,
		Actor:             command.Actor.storePrincipal(),
		Target:            command.Target.storeTarget(),
		Body:              command.Body,
		MentionedAgentIDs: command.MentionedAgentIDs,
		Now:               command.Now,
	})
	if err != nil {
		return MessageSnapshot{}, err
	}
	return newMessageSnapshot(message), nil
}

func (value PrincipalRef) storePrincipal() store.Principal {
	return store.Principal{Kind: authoritydomain.PrincipalKind(value.Kind), ID: value.ID, OrganizationID: value.OrganizationID}
}

func (value MessageTargetRef) storeTarget() store.MessageTarget {
	return store.MessageTarget{Kind: string(value.Kind), ID: value.ID}
}

func (command ChangeMemberCommand) storeParams() store.ChangeMemberParams {
	return store.ChangeMemberParams{
		RequestID: command.RequestID,
		Actor:     command.Actor.storePrincipal(),
		SpaceID:   command.SpaceID,
		Member:    command.Member.storePrincipal(),
		Now:       command.Now,
	}
}

func (command ChangeSpaceArchiveCommand) storeParams() store.ChangeSpaceArchiveParams {
	return store.ChangeSpaceArchiveParams{
		RequestID: command.RequestID,
		Actor:     command.Actor.storePrincipal(),
		SpaceID:   command.SpaceID,
		Now:       command.Now,
	}
}

func newSpaceSnapshot(value store.Space) SpaceSnapshot {
	return SpaceSnapshot{
		ID: value.ID, OrganizationID: value.OrganizationID, Kind: collaborationdomain.SpaceKind(value.Kind), Name: value.Name,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt,
	}
}

func newMessageSnapshot(value store.Message) MessageSnapshot {
	return MessageSnapshot{
		ID: value.ID, RequestID: value.RequestID, SpaceID: value.SpaceID,
		Target: MessageTargetRef{Kind: collaborationdomain.MessageTargetKind(value.Target.Kind), ID: value.Target.ID}, TargetSequence: value.TargetSequence,
		Author: PrincipalRef{Kind: collaborationdomain.PrincipalKind(value.Author.Kind), ID: value.Author.ID, OrganizationID: value.Author.OrganizationID},
		Body:   value.Body, MentionedAgentIDs: append([]string(nil), value.MentionedAgentIDs...), CreatedAt: value.CreatedAt,
	}
}

func newReceiptSnapshot(value store.MutationReceipt) ReceiptSnapshot {
	return ReceiptSnapshot{RequestID: value.RequestID, CommittedAt: value.CommittedAt}
}

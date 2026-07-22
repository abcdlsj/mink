package collaboration

import (
	"context"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
)

type PrincipalRef = authoritydomain.Principal

type MessageTargetRef = collaborationapp.MessageTarget

const (
	TargetSpace  = collaborationdomain.TargetSpace
	TargetThread = collaborationdomain.TargetThread
)

type SpaceSnapshot = collaborationapp.Space

type MessageSnapshot = collaborationapp.Message

type ReceiptSnapshot = collaborationapp.MutationReceipt

type CreateDMCommand = collaborationapp.CreateDMCommand

type CreateGroupCommand = collaborationapp.CreateGroupCommand

type ChangeMemberCommand = collaborationapp.ChangeMemberCommand

type ChangeSpaceArchiveCommand = collaborationapp.ChangeSpaceArchiveCommand

type SendMessageCommand = collaborationapp.SendMessageCommand

func (s *Service) createDM(ctx context.Context, command CreateDMCommand) (SpaceSnapshot, error) {
	return s.store.CreateDM(ctx, command)
}

func (s *Service) createGroup(ctx context.Context, command CreateGroupCommand) (SpaceSnapshot, error) {
	return s.store.CreateGroup(ctx, command)
}

func (s *Service) addMember(ctx context.Context, command ChangeMemberCommand) (ReceiptSnapshot, error) {
	return s.store.AddMember(ctx, command)
}

func (s *Service) removeMember(ctx context.Context, command ChangeMemberCommand) (ReceiptSnapshot, error) {
	return s.store.RemoveMember(ctx, command)
}

func (s *Service) archiveSpace(ctx context.Context, command ChangeSpaceArchiveCommand) (ReceiptSnapshot, error) {
	return s.store.ArchiveSpace(ctx, command)
}

func (s *Service) unarchiveSpace(ctx context.Context, command ChangeSpaceArchiveCommand) (ReceiptSnapshot, error) {
	return s.store.UnarchiveSpace(ctx, command)
}

func (s *Service) sendMessage(ctx context.Context, command SendMessageCommand) (MessageSnapshot, error) {
	return s.store.SendMessage(ctx, command)
}

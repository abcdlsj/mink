package collaboration

import (
	"context"

	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
)

type collaborationStore interface {
	CreateDM(context.Context, collaborationapp.CreateDMCommand) (collaborationapp.Space, error)
	CreateGroup(context.Context, collaborationapp.CreateGroupCommand) (collaborationapp.Space, error)
	GetSpace(context.Context, collaborationapp.SpaceReadQuery) (collaborationapp.Space, error)
	ListSpaces(context.Context, collaborationapp.ListSpacesQuery) ([]collaborationapp.Space, error)
	AddMember(context.Context, collaborationapp.ChangeMemberCommand) (collaborationapp.MutationReceipt, error)
	RemoveMember(context.Context, collaborationapp.ChangeMemberCommand) (collaborationapp.MutationReceipt, error)
	ListMembers(context.Context, collaborationapp.SpaceReadQuery) ([]collaborationapp.Membership, error)
	ArchiveSpace(context.Context, collaborationapp.ChangeSpaceArchiveCommand) (collaborationapp.MutationReceipt, error)
	UnarchiveSpace(context.Context, collaborationapp.ChangeSpaceArchiveCommand) (collaborationapp.MutationReceipt, error)
	SendMessage(context.Context, collaborationapp.SendMessageCommand) (collaborationapp.Message, error)
	GetMessage(context.Context, collaborationapp.GetMessageQuery) (collaborationapp.Message, error)
	GetThread(context.Context, collaborationapp.GetThreadQuery) (collaborationapp.Thread, error)
	ListMessages(context.Context, collaborationapp.ListMessagesQuery) ([]collaborationapp.Message, error)
}

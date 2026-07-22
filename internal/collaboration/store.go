package collaboration

import (
	"context"

	"github.com/abcdlsj/sumi/internal/store"
)

type collaborationStore interface {
	CreateDM(context.Context, store.CreateDMParams) (store.Space, error)
	CreateGroup(context.Context, store.CreateGroupParams) (store.Space, error)
	GetSpace(context.Context, store.SpaceReadParams) (store.Space, error)
	ListSpaces(context.Context, store.ListSpacesParams) ([]store.Space, error)
	AddMember(context.Context, store.ChangeMemberParams) (store.MutationReceipt, error)
	RemoveMember(context.Context, store.ChangeMemberParams) (store.MutationReceipt, error)
	ListMembers(context.Context, store.SpaceReadParams) ([]store.Membership, error)
	ArchiveSpace(context.Context, store.ChangeSpaceArchiveParams) (store.MutationReceipt, error)
	UnarchiveSpace(context.Context, store.ChangeSpaceArchiveParams) (store.MutationReceipt, error)
	SendMessage(context.Context, store.SendMessageParams) (store.Message, error)
	GetMessage(context.Context, store.GetMessageParams) (store.Message, error)
	GetThread(context.Context, store.GetThreadParams) (store.Thread, error)
	ListMessages(context.Context, store.ListMessagesParams) ([]store.Message, error)
}

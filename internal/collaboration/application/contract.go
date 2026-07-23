package application

import (
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
)

type Space struct {
	ID             string
	OrganizationID string
	Kind           collaborationdomain.SpaceKind
	Name           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ArchivedAt     *time.Time
}

type Membership struct {
	SpaceID   string
	Principal authoritydomain.Principal
	JoinedAt  time.Time
}

type Thread struct {
	ID        string
	SpaceID   string
	CreatedAt time.Time
}

type MessageTarget struct {
	Kind collaborationdomain.MessageTargetKind
	ID   string
}

type Message struct {
	ID                  string
	RequestID           string
	SpaceID             string
	Target              MessageTarget
	TargetSequence      uint64
	Author              authoritydomain.Principal
	Body                string
	MentionedPrincipals []authoritydomain.Principal
	CreatedAt           time.Time
}

type MutationReceipt struct {
	RequestID   string
	CommittedAt time.Time
}

type CreateDMCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	Peer      authoritydomain.Principal
	Now       time.Time
}

type CreateGroupCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	Name      string
	Now       time.Time
}

type SpaceReadQuery struct {
	Actor   authoritydomain.Principal
	SpaceID string
	Now     time.Time
}

type ListSpacesQuery struct {
	Actor authoritydomain.Principal
	Now   time.Time
}

type ChangeMemberCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	SpaceID   string
	Member    authoritydomain.Principal
	Now       time.Time
}

type ChangeSpaceArchiveCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	SpaceID   string
	Now       time.Time
}

type SendMessageCommand struct {
	RequestID           string
	Actor               authoritydomain.Principal
	Runtime             authorityapp.RuntimeAuthentication
	Target              MessageTarget
	Body                string
	MentionedPrincipals []authoritydomain.Principal
	Now                 time.Time
}

type GetMessageQuery struct {
	Actor     authoritydomain.Principal
	Runtime   authorityapp.RuntimeAuthentication
	MessageID string
	Now       time.Time
}

type GetThreadQuery struct {
	Actor    authoritydomain.Principal
	Runtime  authorityapp.RuntimeAuthentication
	ThreadID string
	Now      time.Time
}

type ListMessagesQuery struct {
	Actor         authoritydomain.Principal
	Runtime       authorityapp.RuntimeAuthentication
	Target        MessageTarget
	AfterSequence uint64
	Limit         uint32
	Now           time.Time
}

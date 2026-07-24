package application

import (
	"crypto/sha256"
	"io"
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

const (
	GrantRead        = "read"
	GrantManage      = "manage"
	GrantTargetAgent = "agent"
	GrantTargetSpace = "space"
	GrantTargetWork  = "work"
	SourceMessage    = "message"
	SourceVersion    = "artifact_version"
)

type Authentication struct {
	Human authoritydomain.Principal
	Agent authorityapp.RuntimeAuthentication
}

type Artifact struct {
	ID             string
	OrganizationID string
	OwningWorkID   string
	Name           string
	MediaType      string
	Creator        authoritydomain.Principal
	CreatedAt      time.Time
}

type Version struct {
	ArtifactID     string
	OrganizationID string
	Version        uint64
	Digest         [sha256.Size]byte
	Size           int64
	IntegrityState string
	Summary        string
	Author         authoritydomain.Principal
	CreatedAt      time.Time
	Execution      *Execution
	Sources        []Source
}

type Execution struct {
	RunID                    string
	Attempt                  uint64
	AgentID                  string
	ComputerID               string
	PlacementDesiredRevision uint64
	Fence                    uint64
}

type ExecutionInput struct {
	RunID   string
	Attempt uint64
	Fence   uint64
}

type SourceInput struct {
	Kind            string
	MessageID       string
	ArtifactID      string
	ArtifactVersion uint64
}

type Source struct {
	Kind            string
	MessageID       string
	ArtifactID      string
	ArtifactVersion uint64
}

type PublishCommand struct {
	RequestID      string
	Authentication Authentication
	ArtifactID     string
	OwningWorkID   string
	Name           string
	MediaType      string
	Summary        string
	Execution      *ExecutionInput
	Sources        []SourceInput
	ExpectedDigest *[sha256.Size]byte
	ExpectedSize   *int64
	Content        io.Reader
	Now            time.Time
}

type PublishResult struct {
	Artifact    Artifact
	Version     Version
	CommittedAt time.Time
}

type Grant struct {
	ID             string
	ArtifactID     string
	OrganizationID string
	TargetKind     string
	TargetID       string
	Capability     string
	GrantedBy      authoritydomain.Principal
	GrantedAt      time.Time
	RevokedBy      *authoritydomain.Principal
	RevokedAt      *time.Time
}

type GrantCommand struct {
	RequestID      string
	Authentication Authentication
	ArtifactID     string
	TargetKind     string
	TargetID       string
	Capability     string
	Now            time.Time
}

type RevokeGrantCommand struct {
	RequestID      string
	Authentication Authentication
	GrantID        string
	Now            time.Time
}

type SourceView struct {
	Restricted      bool
	Kind            string
	MessageID       string
	ArtifactID      string
	ArtifactVersion uint64
}

type ExecutionView struct {
	Restricted               bool
	RunID                    string
	Attempt                  uint64
	AgentID                  string
	ComputerID               string
	PlacementDesiredRevision uint64
	Fence                    uint64
}

type View struct {
	Artifact
	OwningWorkRestricted bool
	Version              VersionView
}

type VersionView struct {
	ArtifactID     string
	OrganizationID string
	Version        uint64
	Digest         [sha256.Size]byte
	Size           int64
	IntegrityState string
	Summary        string
	Author         authoritydomain.Principal
	CreatedAt      time.Time
	Execution      *ExecutionView
	Sources        []SourceView
}

type GetQuery struct {
	Authentication Authentication
	ArtifactID     string
	Version        uint64
	Now            time.Time
}

type ListQuery struct {
	Authentication  Authentication
	OwningWorkID    string
	AfterArtifactID string
	Limit           uint32
	Now             time.Time
}

type ListResult struct {
	Views          []View
	NextArtifactID string
}

type FetchQuery struct {
	Authentication Authentication
	ArtifactID     string
	Version        uint64
	Now            time.Time
}

type FetchResult struct {
	Artifact Artifact
	Version  VersionView
	Content  io.ReadCloser
}

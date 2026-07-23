package application

import (
	"crypto/sha256"
	"errors"
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

const (
	SourceMessage         = "message"
	SourceWork            = "work"
	SourceArtifactVersion = "artifact_version"

	IndexReady    = "ready"
	IndexDegraded = "degraded"
)

type Source struct {
	Kind    string
	ID      string
	Version uint64
}

type DirtySource struct {
	Sequence uint64
	Source   Source
	Revision [sha256.Size]byte
	Enqueued time.Time
}

type IndexState struct {
	AppliedSequence uint64
	Status          string
}

type IndexHealth uint8

const (
	IndexHealthy IndexHealth = iota
	IndexLagging
	IndexCorrupt
)

type SourceDocument struct {
	Source   Source
	Revision [sha256.Size]byte
	Body     string
}

type SearchQuery struct {
	Human authoritydomain.Principal
	Agent authorityapp.RuntimeAuthentication
	Query string
	Limit uint32
	Now   time.Time
}

type SearchResult struct {
	Source  Source
	Snippet string
}

type SearchOutput struct {
	Results []SearchResult
	Status  string
}

var (
	ErrSearchInvalid         = errors.New("knowledge search input is invalid")
	ErrSearchUnauthenticated = errors.New("knowledge search authentication is invalid")
)

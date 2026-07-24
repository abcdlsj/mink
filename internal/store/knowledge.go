package store

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"

	knowledgeapp "github.com/abcdlsj/sumi/internal/knowledge/application"
	"github.com/google/uuid"
)

const (
	KnowledgeSourceMessage         = knowledgeapp.SourceMessage
	KnowledgeSourceWork            = knowledgeapp.SourceWork
	KnowledgeSourceArtifactVersion = knowledgeapp.SourceArtifactVersion

	KnowledgeIndexReady    = knowledgeapp.IndexReady
	KnowledgeIndexDegraded = knowledgeapp.IndexDegraded

	knowledgeFTSSchema = `CREATE VIRTUAL TABLE knowledge_fts USING fts5(
		source_kind UNINDEXED,
		source_id UNINDEXED,
		source_version UNINDEXED,
		revision UNINDEXED,
		body
	)`

	knowledgeSearchQueryMaxBytes     = 256
	knowledgeSearchTermsMax          = 8
	knowledgeSearchTermMaxBytes      = 64
	knowledgeSearchDefaultLimit      = 20
	knowledgeSearchMaxLimit          = 50
	knowledgeSearchCandidateLimit    = 400
	knowledgeSearchSnippetMaxBytes   = 512
	knowledgeSearchAggregateMaxBytes = 16 << 10
)

type KnowledgeSource = knowledgeapp.Source
type KnowledgeDirtySource = knowledgeapp.DirtySource
type KnowledgeIndexState = knowledgeapp.IndexState
type KnowledgeIndexHealth = knowledgeapp.IndexHealth

const (
	KnowledgeIndexHealthy = knowledgeapp.IndexHealthy
	KnowledgeIndexLagging = knowledgeapp.IndexLagging
	KnowledgeIndexCorrupt = knowledgeapp.IndexCorrupt
)

var errKnowledgeProjectionInvariant = errors.New("knowledge projection invariant violated")

type KnowledgeSourceDocument = knowledgeapp.SourceDocument
type KnowledgeSearchParams = knowledgeapp.SearchQuery
type KnowledgeSearchResult = knowledgeapp.SearchResult
type KnowledgeSearchOutput = knowledgeapp.SearchOutput

var (
	ErrKnowledgeSearchInvalid         = knowledgeapp.ErrSearchInvalid
	ErrKnowledgeSearchUnauthenticated = knowledgeapp.ErrSearchUnauthenticated
	errKnowledgeSearchCorrupt         = errors.New("knowledge search index is corrupt")
)

type KnowledgeWorkFields struct {
	Goal               string
	AcceptanceCriteria []string
	Constraints        []string
	BlockingReason     string
	Result             string
}

func KnowledgeWorkRevision(fields KnowledgeWorkFields) [sha256.Size]byte {
	hash := sha256.New()
	writeKnowledgeField(hash, "goal", fields.Goal)
	writeKnowledgeList(hash, "acceptance_criteria", fields.AcceptanceCriteria)
	writeKnowledgeList(hash, "constraints", fields.Constraints)
	writeKnowledgeField(hash, "blocking_reason", fields.BlockingReason)
	writeKnowledgeField(hash, "result", fields.Result)
	return knowledgeHash(hash)
}

func knowledgeWorkRevision(work Work) [sha256.Size]byte {
	criteria := make([]string, len(work.AcceptanceCriteria))
	for index, criterion := range work.AcceptanceCriteria {
		criteria[index] = criterion.Body
	}
	constraints := make([]string, len(work.Constraints))
	for index, constraint := range work.Constraints {
		constraints[index] = constraint.Body
	}
	return KnowledgeWorkRevision(KnowledgeWorkFields{
		Goal: work.Goal, AcceptanceCriteria: criteria, Constraints: constraints,
		BlockingReason: work.BlockingReason, Result: work.Result,
	})
}

func KnowledgeMessageRevision(messageID string, targetSequence uint64) [sha256.Size]byte {
	hash := sha256.New()
	writeKnowledgeField(hash, "message_id", messageID)
	writeKnowledgeUint64(hash, targetSequence)
	return knowledgeHash(hash)
}

func KnowledgeArtifactVersionRevision(artifactID string, version uint64, digest [sha256.Size]byte) [sha256.Size]byte {
	hash := sha256.New()
	writeKnowledgeField(hash, "artifact_id", artifactID)
	writeKnowledgeUint64(hash, version)
	writeKnowledgeBytes(hash, digest[:])
	return knowledgeHash(hash)
}

func validateKnowledgeSource(source KnowledgeSource) error {
	parsed, err := uuid.Parse(source.ID)
	if err != nil || parsed.String() != source.ID {
		return errors.New("invalid knowledge source id")
	}
	switch source.Kind {
	case KnowledgeSourceMessage, KnowledgeSourceWork:
		if source.Version != 0 {
			return errors.New("knowledge source cannot have a version")
		}
	case KnowledgeSourceArtifactVersion:
		if source.Version == 0 {
			return errors.New("knowledge artifact source version must be positive")
		}
	default:
		return errors.New("unknown knowledge source kind")
	}
	return nil
}

func writeKnowledgeField(hash interface{ Write([]byte) (int, error) }, name, value string) {
	writeKnowledgeBytes(hash, []byte(name))
	writeKnowledgeBytes(hash, []byte(value))
}

func writeKnowledgeList(hash interface{ Write([]byte) (int, error) }, name string, values []string) {
	writeKnowledgeBytes(hash, []byte(name))
	writeKnowledgeUint64(hash, uint64(len(values)))
	for _, value := range values {
		writeKnowledgeBytes(hash, []byte(value))
	}
}

func writeKnowledgeBytes(hash interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	hash.Write(length[:])
	hash.Write(value)
}

func writeKnowledgeUint64(hash interface{ Write([]byte) (int, error) }, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	hash.Write(encoded[:])
}

func knowledgeHash(hash interface{ Sum([]byte) []byte }) [sha256.Size]byte {
	var revision [sha256.Size]byte
	copy(revision[:], hash.Sum(nil))
	return revision
}

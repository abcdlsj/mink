package store

import artifactapp "github.com/abcdlsj/sumi/internal/artifact/application"

var (
	ErrArtifactInvalid           = artifactapp.ErrInvalid
	ErrArtifactNotFound          = artifactapp.ErrNotFound
	ErrArtifactVersionNotFound   = artifactapp.ErrVersionNotFound
	ErrArtifactGrantNotFound     = artifactapp.ErrGrantNotFound
	ErrArtifactRequestConflict   = artifactapp.ErrRequestConflict
	ErrArtifactCursorUnavailable = artifactapp.ErrCursorUnavailable
	ErrArtifactIntegrity         = artifactapp.ErrIntegrity
	ErrArtifactBlobUnavailable   = artifactapp.ErrBlobUnavailable
)

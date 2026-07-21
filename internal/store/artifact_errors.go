package store

import "errors"

var (
	ErrArtifactInvalid         = errors.New("artifact input is invalid")
	ErrArtifactNotFound        = errors.New("artifact not found")
	ErrArtifactVersionNotFound = errors.New("artifact version not found")
	ErrArtifactGrantNotFound   = errors.New("artifact grant not found")
	ErrArtifactRequestConflict = errors.New("artifact request conflicts with committed request")
	ErrArtifactIntegrity       = errors.New("artifact content integrity failure")
	ErrArtifactBlobUnavailable = errors.New("artifact blob store is unavailable")
)

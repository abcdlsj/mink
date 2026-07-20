package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abcdlsj/sumi/internal/placementcode"
	"github.com/google/uuid"
)

type ProvisionError struct {
	Code string
	Path string
	Err  error
}

func (e *ProvisionError) Error() string {
	return fmt.Sprintf("provision %s: %v", e.Path, e.Err)
}

func (e *ProvisionError) Unwrap() error {
	return e.Err
}

func Provision(dataRoot, agentID string) (string, error) {
	parsed, err := uuid.Parse(agentID)
	if err != nil {
		return "", &ProvisionError{Code: placementcode.AgentHomeInvalid, Path: dataRoot, Err: errors.New("agent id is invalid")}
	}
	canonicalID := parsed.String()
	agentsRoot := filepath.Join(dataRoot, "agents")
	agentHome := filepath.Join(agentsRoot, "agent_"+canonicalID)
	workspace := filepath.Join(agentHome, "workspace")
	for _, directory := range []struct {
		path string
		code string
	}{
		{dataRoot, placementcode.WorkspaceRootInvalid},
		{agentsRoot, placementcode.WorkspaceRootInvalid},
		{agentHome, placementcode.AgentHomeInvalid},
		{workspace, placementcode.WorkspaceInvalid},
	} {
		if err := ensureDirectory(directory.path, directory.code); err != nil {
			return "", err
		}
	}
	return workspace, nil
}

func ErrorCode(err error) string {
	var provisionError *ProvisionError
	if errors.As(err, &provisionError) && placementcode.Valid(provisionError.Code) {
		return provisionError.Code
	}
	return placementcode.WorkspaceIOError
}

func ensureDirectory(path, invalidCode string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return provisionFailure(path, invalidCode, err)
	}
	if err := inspectDirectory(path); err != nil {
		return &ProvisionError{Code: invalidCode, Path: path, Err: err}
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return provisionFailure(path, invalidCode, err)
	}
	if err := inspectDirectory(path); err != nil {
		return &ProvisionError{Code: invalidCode, Path: path, Err: err}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return provisionFailure(path, invalidCode, err)
	}
	if info.Mode().Perm() != 0o700 {
		return &ProvisionError{Code: placementcode.WorkspacePermissionDenied, Path: path, Err: fmt.Errorf("mode is %o", info.Mode().Perm())}
	}
	return nil
}

func inspectDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("path is not a real directory")
	}
	return nil
}

func provisionFailure(path, invalidCode string, err error) error {
	code := placementcode.WorkspaceIOError
	if errors.Is(err, os.ErrPermission) {
		code = placementcode.WorkspacePermissionDenied
	} else if os.IsExist(err) {
		code = invalidCode
	}
	return &ProvisionError{Code: code, Path: path, Err: err}
}

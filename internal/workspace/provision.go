package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	placementfailure "github.com/abcdlsj/sumi/internal/placement/failure"
	"github.com/google/uuid"
)

type ProvisionError struct {
	Code string
	Path string
	Err  error
}

type Layout struct {
	Workspace string
	Home      string
	Cache     string
	Temp      string
}

func (e *ProvisionError) Error() string {
	return fmt.Sprintf("provision %s: %v", e.Path, e.Err)
}

func (e *ProvisionError) Unwrap() error {
	return e.Err
}

func Provision(dataRoot, agentID string) (string, error) {
	layout, err := ProvisionLayout(dataRoot, agentID)
	if err != nil {
		return "", err
	}
	return layout.Workspace, nil
}

func ProvisionLayout(dataRoot, agentID string) (Layout, error) {
	parsed, err := uuid.Parse(agentID)
	if err != nil {
		return Layout{}, &ProvisionError{Code: placementfailure.AgentHomeInvalid, Path: dataRoot, Err: errors.New("agent id is invalid")}
	}
	canonicalID := parsed.String()
	agentsRoot := filepath.Join(dataRoot, "agents")
	agentHome := filepath.Join(agentsRoot, "agent_"+canonicalID)
	workspace := filepath.Join(agentHome, "workspace")
	home := filepath.Join(agentHome, "home")
	cache := filepath.Join(agentHome, "cache")
	temp := filepath.Join(os.TempDir(), "sumi", "agent_"+canonicalID)
	for _, directory := range []struct {
		path string
		code string
	}{
		{dataRoot, placementfailure.WorkspaceRootInvalid},
		{agentsRoot, placementfailure.WorkspaceRootInvalid},
		{agentHome, placementfailure.AgentHomeInvalid},
		{workspace, placementfailure.WorkspaceInvalid},
		{home, placementfailure.AgentHomeInvalid},
		{cache, placementfailure.AgentHomeInvalid},
		{filepath.Dir(temp), placementfailure.WorkspaceRootInvalid},
		{temp, placementfailure.AgentHomeInvalid},
	} {
		if err := ensureDirectory(directory.path, directory.code); err != nil {
			return Layout{}, err
		}
	}
	return Layout{Workspace: workspace, Home: home, Cache: cache, Temp: temp}, nil
}

func ErrorCode(err error) string {
	var provisionError *ProvisionError
	if errors.As(err, &provisionError) && placementfailure.Valid(provisionError.Code) {
		return provisionError.Code
	}
	return placementfailure.WorkspaceIOError
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
		return &ProvisionError{Code: placementfailure.WorkspacePermissionDenied, Path: path, Err: fmt.Errorf("mode is %o", info.Mode().Perm())}
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
	code := placementfailure.WorkspaceIOError
	if errors.Is(err, os.ErrPermission) {
		code = placementfailure.WorkspacePermissionDenied
	} else if os.IsExist(err) {
		code = invalidCode
	}
	return &ProvisionError{Code: code, Path: path, Err: err}
}

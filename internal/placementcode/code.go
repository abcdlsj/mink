package placementcode

const (
	WorkspaceRootInvalid      = "workspace_root_invalid"
	AgentHomeInvalid          = "agent_home_invalid"
	WorkspaceInvalid          = "workspace_invalid"
	WorkspacePermissionDenied = "workspace_permission_denied"
	WorkspaceIOError          = "workspace_io_error"
)

func Valid(code string) bool {
	switch code {
	case WorkspaceRootInvalid, AgentHomeInvalid, WorkspaceInvalid, WorkspacePermissionDenied, WorkspaceIOError:
		return true
	default:
		return false
	}
}

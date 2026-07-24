package failure

const (
	WorkspaceRootInvalid      = "workspace_root_invalid"
	AgentHomeInvalid          = "agent_home_invalid"
	WorkspaceInvalid          = "workspace_invalid"
	WorkspacePermissionDenied = "workspace_permission_denied"
	WorkspaceIOError          = "workspace_io_error"
	RuntimeSpecInvalid        = "runtime_spec_invalid"
	EngineUnavailable         = "engine_unavailable"
	CredentialUnavailable     = "credential_unavailable"
)

func Valid(code string) bool {
	switch code {
	case WorkspaceRootInvalid, AgentHomeInvalid, WorkspaceInvalid, WorkspacePermissionDenied, WorkspaceIOError,
		RuntimeSpecInvalid, EngineUnavailable, CredentialUnavailable:
		return true
	default:
		return false
	}
}

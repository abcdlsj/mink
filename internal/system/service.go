package system

import (
	"context"

	"connectrpc.com/connect"
	systemv1 "github.com/abcdlsj/sumi/gen/go/sumi/system/v1"
)

const Version = "0.1.0"

type Service struct {
	serverID string
}

func New(serverID string) *Service {
	return &Service{serverID: serverID}
}

func (s *Service) GetBootstrap(context.Context, *connect.Request[systemv1.GetBootstrapRequest]) (*connect.Response[systemv1.GetBootstrapResponse], error) {
	return connect.NewResponse(&systemv1.GetBootstrapResponse{
		ServerId:     s.serverID,
		Version:      Version,
		Platforms:    []string{"macos", "linux"},
		Capabilities: []string{"generated-connect-api", "persistent-server-identity", "persistent-agent-computer-facts", "conversation-shell"},
	}), nil
}

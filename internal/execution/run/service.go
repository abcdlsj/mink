package run

import (
	"context"
	"time"

	"github.com/abcdlsj/sumi/gen/go/sumi/run/v1/runv1connect"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

type store interface {
	ListRuns(context.Context, executionapp.ListRunsQuery) (executionapp.ListRunsResult, error)
	GetRun(context.Context, executionapp.GetRunQuery) (executionapp.Run, error)
	ClaimRun(context.Context, executionapp.ClaimRunCommand) (executionapp.Run, error)
	RenewRun(context.Context, executionapp.RenewRunCommand) (executionapp.Run, error)
	CancelRun(context.Context, executionapp.CancelRunCommand) (executionapp.Run, error)
	CompleteRun(context.Context, executionapp.CompleteRunCommand) (executionapp.CompleteRunResult, error)
}

type Service struct {
	store store
	now   func() time.Time
}

var _ runv1connect.RunServiceHandler = (*Service)(nil)

func New(database store) *Service {
	return &Service{store: database, now: time.Now}
}

func Procedures() []string {
	return []string{
		runv1connect.RunServiceListRunsProcedure,
		runv1connect.RunServiceGetRunProcedure,
		runv1connect.RunServiceClaimRunProcedure,
		runv1connect.RunServiceRenewRunProcedure,
		runv1connect.RunServiceCancelRunProcedure,
		runv1connect.RunServiceCompleteRunProcedure,
	}
}

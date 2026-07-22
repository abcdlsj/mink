package delivery

import (
	"context"
	"time"

	"github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1/deliveryv1connect"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

type deliveryStore interface {
	ListDeliveries(context.Context, executionapp.ListDeliveriesQuery) (executionapp.ListDeliveriesResult, error)
	AcceptDelivery(context.Context, executionapp.AcceptDeliveryCommand) (executionapp.Run, error)
	GetRun(context.Context, executionapp.GetRunQuery) (executionapp.Run, error)
	ClaimRun(context.Context, executionapp.ClaimRunCommand) (executionapp.RunLaunch, error)
	RenewRun(context.Context, executionapp.RenewRunCommand) (executionapp.RunLaunch, error)
	CompleteRun(context.Context, executionapp.CompleteRunCommand) (executionapp.CompleteRunResult, error)
}

type Service struct {
	store deliveryStore
	now   func() time.Time
}

var _ deliveryv1connect.DeliveryServiceHandler = (*Service)(nil)

func New(database deliveryStore) *Service {
	return &Service{store: database, now: time.Now}
}

func Procedures() []string {
	return []string{
		deliveryv1connect.DeliveryServiceListDeliveriesProcedure,
		deliveryv1connect.DeliveryServiceAcceptDeliveryProcedure,
		deliveryv1connect.DeliveryServiceGetRunProcedure,
		deliveryv1connect.DeliveryServiceClaimRunProcedure,
		deliveryv1connect.DeliveryServiceRenewRunProcedure,
		deliveryv1connect.DeliveryServiceCompleteRunProcedure,
	}
}

package delivery

import (
	"context"
	"time"

	"github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1/deliveryv1connect"
	"github.com/abcdlsj/sumi/internal/store"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

type deliveryStore interface {
	ListDeliveries(context.Context, store.ListDeliveriesParams) (store.ListDeliveriesResult, error)
	AcceptDelivery(context.Context, store.AcceptDeliveryParams) (store.Run, error)
	GetRun(context.Context, store.GetRunParams) (store.Run, error)
	ClaimRun(context.Context, store.ClaimRunParams) (store.RunLaunch, error)
	RenewRun(context.Context, store.RenewRunParams) (store.RunLaunch, error)
	CompleteRun(context.Context, store.CompleteRunParams) (store.CompleteRunResult, error)
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

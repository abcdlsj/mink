package computer

import (
	"context"

	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
)

type computerStore interface {
	CreateComputerPairing(context.Context, computerapp.PreparePairingCommand) (computerapp.Pairing, error)
	PairComputer(context.Context, computerapp.PairCommand) (computerapp.Computer, error)
	HeartbeatComputer(context.Context, computerapp.HeartbeatCommand) (computerapp.Computer, error)
	GetComputer(context.Context, string) (computerapp.Computer, error)
	ListComputers(context.Context) ([]computerapp.Computer, error)
	EnqueueCredentialDelivery(context.Context, computerapp.EnqueueCredentialDeliveryCommand) (computerapp.CredentialDelivery, error)
	ListCredentialDeliveries(context.Context, computerapp.ListCredentialDeliveriesQuery) ([]computerapp.CredentialDelivery, error)
	ClaimCredentialDelivery(context.Context, computerapp.ClaimCredentialDeliveryCommand) (computerapp.CredentialDelivery, error)
	CompleteCredentialDelivery(context.Context, computerapp.CompleteCredentialDeliveryCommand) (computerapp.CredentialDelivery, error)
}

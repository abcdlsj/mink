package computer

import (
	"context"

	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
)

type computerStore interface {
	CreateComputerPairing(context.Context, computerapp.PreparePairingCommand) (computerapp.Pairing, error)
	PairComputer(context.Context, computerapp.PairCommand) (computerapp.Computer, error)
	RecoverComputer(context.Context, computerapp.RegistrationCommand) (computerapp.Computer, error)
	HeartbeatComputer(context.Context, computerapp.HeartbeatCommand) (computerapp.Computer, error)
	GetComputer(context.Context, string) (computerapp.Computer, error)
	ListComputers(context.Context) ([]computerapp.Computer, error)
}

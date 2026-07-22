package computer

import (
	"context"

	"github.com/abcdlsj/sumi/internal/store"
)

type computerStore interface {
	CreateComputerPairing(context.Context, store.CreateComputerPairingParams) (store.ComputerPairing, error)
	PairComputer(context.Context, store.PairComputerParams) (store.Computer, error)
	RegisterComputer(context.Context, store.RegisterComputerParams) (store.Computer, error)
	RecoverComputer(context.Context, store.RegisterComputerParams) (store.Computer, error)
	HeartbeatComputer(context.Context, store.HeartbeatComputerParams) (store.Computer, error)
	GetComputer(context.Context, string) (store.Computer, error)
	ListComputers(context.Context) ([]store.Computer, error)
}

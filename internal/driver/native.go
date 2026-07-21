package driver

import (
	"context"
	"errors"
)

type Native struct {
	ExecuteFunc func(context.Context, Command, EventSink) (TurnResult, error)
}

func (n Native) Execute(ctx context.Context, command Command, events EventSink) (TurnResult, error) {
	if err := command.validate(); err != nil {
		return TurnResult{}, err
	}
	if n.ExecuteFunc == nil {
		return TurnResult{}, errors.New("native driver is not configured")
	}
	result, err := n.ExecuteFunc(ctx, command, events)
	if err != nil {
		return TurnResult{}, err
	}
	if err := result.validate(); err != nil {
		return TurnResult{}, err
	}
	return result, nil
}

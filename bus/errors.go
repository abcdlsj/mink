package bus

import "fmt"

type RouteError struct {
	Op   string
	Msg  string
	Type string
	From string
	To   string
}

func (e *RouteError) Error() string {
	return fmt.Sprintf("bus %s: %s (type=%s from=%s to=%s)", e.Op, e.Msg, e.Type, e.From, e.To)
}

func ErrInvalidAddr(op, typ, from, to, msg string) error {
	return &RouteError{Op: op, Msg: msg, Type: typ, From: from, To: to}
}


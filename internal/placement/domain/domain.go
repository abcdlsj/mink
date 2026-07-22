package domain

import (
	"errors"

	placementfailure "github.com/abcdlsj/sumi/internal/placement/failure"
)

type State string

const (
	StatePending State = "pending"
	StateActive  State = "active"
	StateFailed  State = "failed"
)

type Acknowledgement struct {
	State     State
	ErrorCode string
}

var (
	ErrAcknowledgementStateInvalid = errors.New("acknowledgement state invalid")
	ErrActiveWithErrorCode         = errors.New("active acknowledgement has error code")
	ErrFailureCodeInvalid          = errors.New("failed acknowledgement error code invalid")
)

func NewAcknowledgement(state State, errorCode string) (Acknowledgement, error) {
	switch state {
	case StateActive:
		if errorCode != "" {
			return Acknowledgement{}, ErrActiveWithErrorCode
		}
	case StateFailed:
		if !placementfailure.Valid(errorCode) {
			return Acknowledgement{}, ErrFailureCodeInvalid
		}
	default:
		return Acknowledgement{}, ErrAcknowledgementStateInvalid
	}
	return Acknowledgement{State: state, ErrorCode: errorCode}, nil
}

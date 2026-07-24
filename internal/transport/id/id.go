package id

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

func CanonicalID(value, field string) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New(field+" must be a UUID"))
	}
	return parsed.String(), nil
}

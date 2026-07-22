package domain

import (
	"errors"
	"testing"

	placementfailure "github.com/abcdlsj/sumi/internal/placement/failure"
)

func TestNewAcknowledgement(t *testing.T) {
	tests := []struct {
		name      string
		state     State
		errorCode string
		want      Acknowledgement
		wantErr   error
	}{
		{"active", StateActive, "", Acknowledgement{State: StateActive}, nil},
		{"failed", StateFailed, placementfailure.WorkspaceInvalid, Acknowledgement{State: StateFailed, ErrorCode: placementfailure.WorkspaceInvalid}, nil},
		{"pending", StatePending, "", Acknowledgement{}, ErrAcknowledgementStateInvalid},
		{"active with error", StateActive, placementfailure.WorkspaceInvalid, Acknowledgement{}, ErrActiveWithErrorCode},
		{"failed without error", StateFailed, "", Acknowledgement{}, ErrFailureCodeInvalid},
		{"failed with unknown error", StateFailed, "unknown", Acknowledgement{}, ErrFailureCodeInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NewAcknowledgement(test.state, test.errorCode)
			if got != test.want || !errors.Is(err, test.wantErr) {
				t.Fatalf("NewAcknowledgement(%q, %q) = %+v, %v; want %+v, %v", test.state, test.errorCode, got, err, test.want, test.wantErr)
			}
		})
	}
}

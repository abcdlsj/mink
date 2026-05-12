package telegram

import (
	"errors"
	"testing"
)

func TestUserErrorHidesTemporaryUpstreamFailure(t *testing.T) {
	err := errors.New("error, status code: 503, status: 503 Service Unavailable, message: Service temporarily unavailable")
	got := userError(err)
	want := "Model service is temporarily unavailable (HTTP 503). Please try again later."
	if got != want {
		t.Fatalf("userError() = %q, want %q", got, want)
	}
}

func TestUserErrorPreservesRegularError(t *testing.T) {
	got := userError(errors.New("boom"))
	if got != "error: boom" {
		t.Fatalf("userError() = %q", got)
	}
}

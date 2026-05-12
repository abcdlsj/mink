package llm

import (
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestWrapOpenAIErrAPIError(t *testing.T) {
	err := wrapOpenAIErr(&openai.APIError{
		Message:        "Service temporarily unavailable",
		HTTPStatus:     "503 Service Unavailable",
		HTTPStatusCode: 503,
	})
	if got, want := err.Error(), "Service temporarily unavailable (HTTP 503)"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

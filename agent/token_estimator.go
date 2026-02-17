package agent

import (
	"sync"

	"github.com/abcdlsj/mink/msg"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

type tokenEstimator struct {
	model string

	mu  sync.Mutex
	enc *tiktoken.Tiktoken
	err error
}

func newTokenEstimator(model string) *tokenEstimator {
	return &tokenEstimator{model: model}
}

func (e *tokenEstimator) setModel(model string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.model == model {
		return
	}
	e.model = model
	e.enc = nil
	e.err = nil
}

func (e *tokenEstimator) encoder() (*tiktoken.Tiktoken, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.enc != nil {
		return e.enc, nil
	}
	if e.err != nil {
		return nil, e.err
	}

	enc, err := tiktoken.EncodingForModel(e.model)
	if err != nil {
		enc, err = tiktoken.GetEncoding("cl100k_base")
	}
	if err != nil {
		e.err = err
		return nil, err
	}
	e.enc = enc
	return enc, nil
}

func (e *tokenEstimator) text(s string) int {
	if s == "" {
		return 0
	}

	enc, err := e.encoder()
	if err != nil {
		return roughTextTokens(s)
	}
	return len(enc.EncodeOrdinary(s))
}

func (e *tokenEstimator) message(m msg.Message) int {
	total := 8
	total += e.text(m.Role)
	total += e.text(m.Content)
	total += e.text(m.Reasoning)

	for _, tc := range m.ToolCalls {
		total += 12
		total += e.text(tc.ID)
		total += e.text(tc.Name)
		total += e.text(string(tc.Args))
	}

	for _, tr := range m.ToolResults {
		total += 10
		total += e.text(tr.ToolCallID)
		total += e.text(tr.Content)
		total += e.text(tr.Error)
	}

	if total < 1 {
		return 1
	}
	return total
}

func (e *tokenEstimator) messages(msgs []msg.Message) int {
	if len(msgs) == 0 {
		return 0
	}

	total := 0
	for _, m := range msgs {
		total += e.message(m)
	}

	if total < 1 {
		return len(msgs) * 12
	}
	return total
}

func roughTextTokens(s string) int {
	r := len([]rune(s))
	if r == 0 {
		return 0
	}
	n := r / 4
	if n < 1 {
		return 1
	}
	return n
}

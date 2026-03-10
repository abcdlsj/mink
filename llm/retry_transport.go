package llm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	llmRetryMaxRetries = 3
	llmRetryBaseDelay  = 500 * time.Millisecond
	llmRetryMaxDelay   = 8 * time.Second
)

type retryTransport struct {
	base      http.RoundTripper
	prepare   func(*http.Request) error
	retries   int
	baseDelay time.Duration
	maxDelay  time.Duration
}

func newRetryHTTPClient(prepare func(*http.Request) error) *http.Client {
	return &http.Client{
		Transport: &retryTransport{prepare: prepare},
	}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := ensureReplayable(req); err != nil {
		return nil, err
	}
	if t.prepare != nil {
		if err := t.prepare(req); err != nil {
			return nil, err
		}
	}

	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	for i := 0; ; i++ {
		r, err := cloneRequest(req)
		if err != nil {
			return nil, err
		}

		resp, err := base.RoundTrip(r)
		if !shouldRetry(resp, err) || i >= t.maxRetries() {
			return resp, err
		}

		closeResponse(resp)
		if err := sleep(req.Context(), retryDelay(i, resp, t.baseBackoff(), t.maxBackoff())); err != nil {
			return nil, err
		}
	}
}

func (t *retryTransport) maxRetries() int {
	if t.retries > 0 {
		return t.retries
	}
	return llmRetryMaxRetries
}

func (t *retryTransport) baseBackoff() time.Duration {
	if t.baseDelay > 0 {
		return t.baseDelay
	}
	return llmRetryBaseDelay
}

func (t *retryTransport) maxBackoff() time.Duration {
	if t.maxDelay > 0 {
		return t.maxDelay
	}
	return llmRetryMaxDelay
}

func ensureReplayable(req *http.Request) error {
	if req.Body == nil || req.GetBody != nil {
		return nil
	}

	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return err
	}

	resetRequestBody(req, body)
	return nil
}

func cloneRequest(req *http.Request) (*http.Request, error) {
	r := req.Clone(req.Context())
	if req.Body == nil {
		return r, nil
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	r.Body = body
	return r, nil
}

func resetRequestBody(req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	req.ContentLength = int64(len(body))
	if len(body) == 0 {
		req.Body = http.NoBody
		req.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
	}
}

func shouldRetry(resp *http.Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	if resp == nil {
		return false
	}

	switch resp.StatusCode {
	case http.StatusRequestTimeout,
		http.StatusConflict,
		http.StatusUnprocessableEntity,
		http.StatusTooEarly,
		http.StatusTooManyRequests:
		return true
	}

	return resp.StatusCode >= http.StatusInternalServerError
}

func retryDelay(n int, resp *http.Response, baseDelay, maxDelay time.Duration) time.Duration {
	delay := baseDelay
	for i := 0; i < n; i++ {
		delay *= 2
		if delay >= maxDelay {
			delay = maxDelay
			break
		}
	}

	if resp == nil {
		return delay
	}
	if d := parseRetryAfter(resp.Header); d > 0 {
		if d > maxDelay {
			return maxDelay
		}
		return d
	}
	return delay
}

func parseRetryAfter(h http.Header) time.Duration {
	if ms := h.Get("Retry-After-Ms"); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil && n > 0 {
			return time.Duration(n) * time.Millisecond
		}
	}

	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}

	when, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	d := time.Until(when)
	if d <= 0 {
		return 0
	}
	return d
}

func closeResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

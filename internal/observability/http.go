package observability

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"time"
)

func HTTPMiddleware(logger *Logger, next http.Handler) http.Handler {
	t := CategoryLogger(logger, ComponentServer, CategoryTransport)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		obs := &respObs{ResponseWriter: w}
		next.ServeHTTP(obs, r)
		status := obs.status
		if status == 0 {
			status = http.StatusOK
		}
		fields := []any{
			"event", "transport.http.completed",
			"method", r.Method,
			"path", safePath(r.URL.Path),
			"status", status,
			"duration", time.Since(started),
			"response_bytes", obs.bytes,
		}
		switch {
		case status >= http.StatusInternalServerError:
			t.Warn("HTTP request failed", fields...)
		case status >= http.StatusBadRequest:
			t.Info("HTTP request rejected", fields...)
		default:
			t.Debug("HTTP request completed", fields...)
		}
	})
}

func safePath(path string) string {
	if strings.HasPrefix(path, "/auth/") {
		return "/auth/:token"
	}
	return path
}

type respObs struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *respObs) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *respObs) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(payload)
	w.bytes += n
	return n, err
}

func (w *respObs) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *respObs) Flush() {
	http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *respObs) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *respObs) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

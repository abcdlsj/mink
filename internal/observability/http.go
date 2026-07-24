package observability

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"time"
)

func HTTPMiddleware(logger *Logger, next http.Handler) http.Handler {
	transport := CategoryLogger(logger, ComponentServer, CategoryTransport)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		observed := &responseObserver{ResponseWriter: response}
		next.ServeHTTP(observed, request)
		status := observed.status
		if status == 0 {
			status = http.StatusOK
		}
		fields := []any{
			"event", "transport.http.completed",
			"method", request.Method,
			"path", safeRequestPath(request.URL.Path),
			"status", status,
			"duration", time.Since(started),
			"response_bytes", observed.bytes,
		}
		switch {
		case status >= http.StatusInternalServerError:
			transport.Warn("HTTP request failed", fields...)
		case status >= http.StatusBadRequest:
			transport.Info("HTTP request rejected", fields...)
		default:
			transport.Debug("HTTP request completed", fields...)
		}
	})
}

func safeRequestPath(path string) string {
	if strings.HasPrefix(path, "/auth/") {
		return "/auth/:token"
	}
	return path
}

type responseObserver struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (response *responseObserver) WriteHeader(status int) {
	if response.status != 0 {
		return
	}
	response.status = status
	response.ResponseWriter.WriteHeader(status)
}

func (response *responseObserver) Write(payload []byte) (int, error) {
	if response.status == 0 {
		response.WriteHeader(http.StatusOK)
	}
	written, err := response.ResponseWriter.Write(payload)
	response.bytes += written
	return written, err
}

func (response *responseObserver) Unwrap() http.ResponseWriter {
	return response.ResponseWriter
}

func (response *responseObserver) Flush() {
	_ = http.NewResponseController(response.ResponseWriter).Flush()
}

func (response *responseObserver) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(response.ResponseWriter).Hijack()
}

func (response *responseObserver) Push(target string, options *http.PushOptions) error {
	pusher, ok := response.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

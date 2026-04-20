package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *server) handleIndex(rw http.ResponseWriter, _ *http.Request) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write([]byte(indexHTML))
}

func (s *server) handleState(rw http.ResponseWriter, _ *http.Request) {
	state, err := s.state()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(rw, state)
}

func (s *server) handleEvents(rw http.ResponseWriter, req *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	id, ch := s.subscribe()
	defer s.unsubscribe(id)
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	fmt.Fprint(rw, "event: state\ndata: refresh\n\n")
	flusher.Flush()
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(rw, ": ping\n\n")
			flusher.Flush()
		case <-ch:
			fmt.Fprint(rw, "event: state\ndata: refresh\n\n")
			flusher.Flush()
		}
	}
}

func (s *server) handleSelect(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := readJSON(req, &in); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.app.SwitchSession(source, in.ID); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	s.notify()
	writeJSON(rw, map[string]bool{"ok": true})
}

func (s *server) handleMessage(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSON(req, &in); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		http.Error(rw, "text is required", http.StatusBadRequest)
		return
	}
	go func() {
		if _, err := s.app.HandleInput(context.Background(), source, text); err != nil {
			s.app.PublishNotice(source, err.Error())
		}
	}()
	s.notify()
	writeJSON(rw, map[string]bool{"ok": true})
}

func (s *server) handleAction(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(req, &in); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	switch strings.TrimSpace(in.Name) {
	case "new_session":
		if _, err := s.app.NewSession(source); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	default:
		http.Error(rw, "unknown action", http.StatusBadRequest)
		return
	}
	s.notify()
	writeJSON(rw, map[string]bool{"ok": true})
}

func readJSON(req *http.Request, dst any) error {
	return json.NewDecoder(req.Body).Decode(dst)
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(rw).Encode(v)
}

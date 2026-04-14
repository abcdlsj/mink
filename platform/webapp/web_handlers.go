package webapp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type webSelectPayload struct {
	Section string `json:"section"`
	ID      string `json:"id"`
}

type webMessagePayload struct {
	Text string `json:"text"`
}

type webActionPayload struct {
	Name string `json:"name"`
}

func (w *Web) handleIndex(rw http.ResponseWriter, _ *http.Request) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write([]byte(webMissingPage))
}

func (w *Web) handleState(rw http.ResponseWriter, _ *http.Request) {
	if w.cb.State == nil {
		http.Error(rw, "state unavailable", http.StatusServiceUnavailable)
		return
	}
	state, err := w.cb.State()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(rw, state)
}

func (w *Web) handleEvents(rw http.ResponseWriter, req *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	rw.Header().Set("X-Accel-Buffering", "no")

	id, ch := w.subscribe()
	defer w.unsubscribe(id)

	fmt.Fprint(rw, "event: state\ndata: ready\n\n")
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-req.Context().Done():
			return
		case <-keepAlive.C:
			fmt.Fprint(rw, ": ping\n\n")
			flusher.Flush()
		case _, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprint(rw, "event: state\ndata: refresh\n\n")
			flusher.Flush()
		}
	}
}

func (w *Web) handleSelect(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.cb.Select == nil {
		http.Error(rw, "selection unavailable", http.StatusServiceUnavailable)
		return
	}
	var payload webSelectPayload
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if err := w.cb.Select(payload.Section, payload.ID); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	w.NotifyStateChanged()
	writeJSON(rw, map[string]bool{"ok": true})
}

func (w *Web) handleMessage(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.cb.SendMessage == nil {
		http.Error(rw, "message send unavailable", http.StatusServiceUnavailable)
		return
	}
	var payload webMessagePayload
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if err := w.cb.SendMessage(payload.Text); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	w.NotifyStateChanged()
	writeJSON(rw, map[string]bool{"ok": true})
}

func (w *Web) handleAction(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload webActionPayload
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if w.cb.Action != nil {
		if err := w.cb.Action(payload.Name); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		w.NotifyStateChanged()
		writeJSON(rw, map[string]bool{"ok": true})
		return
	}
	switch payload.Name {
	case "new_session":
		if w.cb.NewSession == nil {
			http.Error(rw, "action unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := w.cb.NewSession(); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	default:
		http.Error(rw, "unknown action", http.StatusBadRequest)
		return
	}
	w.NotifyStateChanged()
	writeJSON(rw, map[string]bool{"ok": true})
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(rw).Encode(v)
}

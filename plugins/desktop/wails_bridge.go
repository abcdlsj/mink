package desktop

import (
	"context"
	"embed"
	"io/fs"
	"net/http"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/abcdlsj/sumi/app"
)

//go:embed all:frontend/dist
var wailsAssets embed.FS

type WailsBridge struct {
	*Backend
	ctx context.Context
}

func NewWailsBridge(a *app.App) *WailsBridge {
	return &WailsBridge{Backend: newBackend(a)}
}

func (w *WailsBridge) Assets() fs.FS {
	sub, err := fs.Sub(wailsAssets, "frontend/dist")
	if err != nil {
		return wailsAssets
	}
	return sub
}

func (w *WailsBridge) Handler() http.Handler {
	return w.Backend.APIHandler(false)
}

func (w *WailsBridge) Start(ctx context.Context) {
	w.ctx = ctx
	w.Backend.start(ctx)
	go w.pump()
}

func (w *WailsBridge) Close() {
}

func (w *WailsBridge) pump() {
	events, cancel := w.Backend.Subscribe()
	defer cancel()
	for {
		select {
		case <-w.ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			runtime.EventsEmit(w.ctx, "bus", ev)
		}
	}
}

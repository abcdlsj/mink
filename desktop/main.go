package main

import (
	"context"
	"embed"

	"github.com/abcdlsj/sumi/internal/desktop"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var bootstrapAssets embed.FS

type wailsShell struct{}

func (wailsShell) ExecJS(ctx context.Context, script string) {
	runtime.WindowExecJS(ctx, script)
}

func main() {
	app, err := desktop.New(wailsShell{})
	if err != nil {
		println("Sumi Desktop failed to initialize.")
		return
	}
	err = wails.Run(&options.App{
		Title:       "Sumi",
		Width:       1200,
		Height:      800,
		MinWidth:    900,
		MinHeight:   640,
		AssetServer: &assetserver.Options{Assets: bootstrapAssets},
		BackgroundColour: &options.RGBA{
			R: 247,
			G: 248,
			B: 250,
			A: 255,
		},
		OnDomReady:             app.DomReady,
		BindingsAllowedOrigins: desktop.DefaultOrigin,
		DragAndDrop: &options.DragAndDrop{
			DisableWebViewDrop: true,
		},
	})
	if err != nil {
		println("Sumi Desktop stopped unexpectedly.")
	}
}

package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/abcdlsj/mink/bus"
)

func runCLI(ctx context.Context, a *App, args []string) error {
	return runCLIWithIO(ctx, a, "cli", os.Stdin, os.Stdout)
}

func runCLIWithIO(ctx context.Context, a *App, source string, in io.Reader, out io.Writer) error {
	events, cancel := a.bus.Subscribe(256)
	defer cancel()
	ui := &cliUI{
		app:    a,
		source: source,
		out:    out,
	}

	scanner := bufio.NewScanner(in)
	ui.header()
	for {
		ui.prompt()
		if !scanner.Scan() {
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return nil
		}
		ui.beginTurn()
		done := make(chan cliResult, 1)
		go func() {
			reply, err := a.HandleInput(ctx, source, line)
			done <- cliResult{reply: reply, err: err}
		}()

		var res cliResult
		var got, end bool
		for {
			select {
			case res = <-done:
				got = true
			case ev, ok := <-events:
				if !ok {
					return nil
				}
				if ev.Source != source {
					continue
				}
				if handleCLIEvent(ui, ev) {
					end = true
				}
			}
			if got && (end || res.err != nil) {
				break
			}
		}

		if res.err != nil {
			if !end {
				ui.line("error: " + res.err.Error())
			}
			continue
		}
		ui.finishTurn(res.reply, strings.HasPrefix(line, "!"))
	}
}

type cliResult struct {
	reply string
	err   error
}

func handleCLIEvent(ui *cliUI, ev bus.Event) bool {
	switch ev.Type {
	case bus.TurnChunk:
		ui.chunk(ev.Text)
	case bus.ToolCallStarted:
		input := strings.TrimSpace(ev.Input)
		if input == "" {
			ui.line("[tool] " + ev.Tool)
		} else {
			ui.line("[tool] " + ev.Tool + " " + input)
		}
	case bus.ToolCallFinished:
		if strings.TrimSpace(ev.Output) != "" {
			ui.line("[tool:" + ev.Tool + "] " + strings.TrimSpace(ev.Output))
		}
	case bus.ToolCallFailed:
		ui.line("[tool:" + ev.Tool + "] error: " + ev.Err)
	case bus.TurnFinished:
		ui.breakLine()
		return true
	case bus.TurnError:
		ui.line("error: " + ev.Err)
		return true
	case bus.CommandHandled:
		return true
	case bus.ServiceNotice:
		ui.line(ev.Text)
	}
	return false
}

type cliUI struct {
	app      *App
	source   string
	out      io.Writer
	streamed bool
	lineOpen bool
}

func (u *cliUI) header() {
	st := u.state()
	fmt.Fprintf(u.out, "mink\nruntime: %s\nmodel: %s\ncwd: %s\nsession: %s\n\n", st.Runtime, st.Model, st.Cwd, st.Session)
}

func (u *cliUI) prompt() {
	fmt.Fprint(u.out, "> ")
}

func (u *cliUI) beginTurn() {
	u.streamed = false
	u.lineOpen = false
}

func (u *cliUI) chunk(s string) {
	if s == "" {
		return
	}
	fmt.Fprint(u.out, s)
	u.streamed = true
	u.lineOpen = !strings.HasSuffix(s, "\n")
}

func (u *cliUI) breakLine() {
	u.breakLineLocked()
}

func (u *cliUI) line(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	u.breakLineLocked()
	fmt.Fprintln(u.out, s)
	u.lineOpen = false
}

func (u *cliUI) finishTurn(reply string, force bool) {
	reply = strings.TrimSpace(reply)
	u.breakLineLocked()
	if reply != "" && (force || !u.streamed) {
		fmt.Fprintln(u.out, reply)
	}
	u.lineOpen = false
}

func (u *cliUI) breakLineLocked() {
	if !u.lineOpen {
		return
	}
	fmt.Fprintln(u.out)
	u.lineOpen = false
}

func (u *cliUI) state() cliState {
	rt := strings.TrimSpace(u.app.cfg.Runtime)
	if rt == "" {
		rt = "native"
	}
	model := u.app.CurrentModel()
	switch rt {
	case "claude":
		model = "claude native"
	case "codex":
		model = "codex native"
	}
	ws := strings.TrimSpace(u.app.Workspace())
	if ws == "" {
		ws = "."
	}
	sid := "(new)"
	if s, err := u.app.CurrentSession(u.source); err == nil && s != nil && s.ID != "" {
		sid = s.ID
	}
	return cliState{
		Runtime: rt,
		Model:   model,
		Cwd:     filepath.Clean(ws),
		Session: sid,
	}
}

type cliState struct {
	Runtime string
	Model   string
	Cwd     string
	Session string
}

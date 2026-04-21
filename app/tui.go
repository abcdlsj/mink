package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/textutil"
)

func runCLI(ctx context.Context, a *App, args []string) error {
	ui, err := newTUI(ctx, a, "cli")
	if err != nil {
		return err
	}
	return ui.run()
}

type tui struct {
	ctx    context.Context
	app    *App
	source string

	tv         *tview.Application
	pages      *tview.Pages
	status     *tview.TextView
	transcript *tview.List
	inspector  *tview.TextView
	input      *tview.TextArea

	events <-chan bus.Event
	cancel func()

	approver *tuiApprover

	items     []*tuiItem
	toolItems map[string]int
	turn      tuiTurn

	busy bool
}

type tuiTurn struct {
	assistantIndex int
	streamed       bool
	commandHandled bool
	errored        bool
	toolCount      int
}

func newTUI(ctx context.Context, a *App, source string) (*tui, error) {
	events, cancel := a.Bus().Subscribe(256)
	ui := &tui{
		ctx:        ctx,
		app:        a,
		source:     source,
		tv:         tview.NewApplication(),
		pages:      tview.NewPages(),
		status:     tview.NewTextView(),
		transcript: tview.NewList(),
		inspector:  tview.NewTextView(),
		input:      tview.NewTextArea(),
		events:     events,
		cancel:     cancel,
		toolItems:  map[string]int{},
		turn:       tuiTurn{assistantIndex: -1},
	}
	ui.approver = newTUIApprover(ui)
	ui.app.SetToolApprover(ui.approver)
	ui.build()
	return ui, nil
}

func (u *tui) run() error {
	defer u.cancel()
	defer u.app.SetToolApprover(nil)

	go u.consumeEvents()
	return u.tv.SetRoot(u.pages, true).EnableMouse(true).Run()
}

func (u *tui) build() {
	u.status.SetDynamicColors(false)
	u.status.SetTextAlign(tview.AlignLeft)
	u.status.SetBorder(false)

	u.transcript.ShowSecondaryText(true)
	u.transcript.SetHighlightFullLine(true)
	u.transcript.SetWrapAround(false)
	u.transcript.SetBorder(false)
	u.transcript.SetChangedFunc(func(index int, main, secondary string, shortcut rune) {
		u.renderInspector(index)
	})

	u.inspector.SetBorder(true)
	u.inspector.SetTitle("Details")
	u.inspector.SetWrap(true)
	u.inspector.SetWordWrap(true)
	u.inspector.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEsc, tcell.KeyF2:
			u.pages.RemovePage("detail")
			u.tv.SetFocus(u.transcript)
			return nil
		}
		return ev
	})

	u.input.SetBorder(true)
	u.input.SetTitle("Compose")
	u.input.SetPlaceholder("Ctrl+S send, Enter newline, Tab focus, F2 details")
	u.input.SetInputCapture(u.captureInput)

	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(u.transcript, 0, 1, false).
		AddItem(u.input, 6, 0, true).
		AddItem(u.status, 1, 0, false)

	u.pages.AddPage("main", root, true, true)
	u.tv.SetInputCapture(u.captureGlobal)
	u.tv.SetFocus(u.input)
	u.refreshStatus()
	u.renderInspector(-1)
}

func (u *tui) captureGlobal(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlC:
		u.tv.Stop()
		return nil
	case tcell.KeyTAB:
		u.cycleFocus()
		return nil
	case tcell.KeyF1:
		u.showHelp()
		return nil
	case tcell.KeyF2:
		u.toggleDetail()
		return nil
	}
	return ev
}

func (u *tui) captureInput(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlS:
		u.submit()
		return nil
	}
	return ev
}

func (u *tui) cycleFocus() {
	switch {
	case u.input.HasFocus():
		u.tv.SetFocus(u.transcript)
	default:
		u.tv.SetFocus(u.input)
	}
}

func (u *tui) showHelp() {
	modal := tview.NewModal().
		SetText("Mink TUI\n\nCtrl+S send input\nTab switch focus\nF1 help\nF2 open details for the current item\nCtrl+C quit\n\nThe main view keeps the conversation clean. Full tool arguments and outputs stay in the details panel.").
		AddButtons([]string{"Close"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			u.pages.RemovePage("help")
			u.tv.SetFocus(u.input)
		})
	u.pages.AddPage("help", modal, true, true)
	u.tv.SetFocus(modal)
}

func (u *tui) toggleDetail() {
	if u.pages.HasPage("detail") {
		u.pages.RemovePage("detail")
		u.tv.SetFocus(u.transcript)
		return
	}
	u.pages.AddPage("detail", u.inspector, true, true)
	u.tv.SetFocus(u.inspector)
}

func (u *tui) submit() {
	if u.busy {
		u.addItem(tuiItem{
			Kind:    tuiNotice,
			Content: "The current turn is still running. Wait for it to finish before sending another input.",
			Time:    time.Now(),
		})
		return
	}
	text := textutil.Valid(strings.TrimSpace(u.input.GetText()))
	if text == "" {
		return
	}
	u.input.SetText("", true)
	u.busy = true
	u.turn = tuiTurn{assistantIndex: -1}
	u.addItem(tuiItem{
		Kind:    tuiUser,
		Content: text,
		Time:    time.Now(),
	})
	u.refreshStatus()

	go func(input string) {
		reply, err := u.app.HandleInput(u.ctx, u.source, input)
		u.tv.QueueUpdateDraw(func() {
			u.finishTurn(reply, err)
		})
	}(text)
}

func (u *tui) finishTurn(reply string, err error) {
	u.busy = false
	defer u.refreshStatus()

	if err != nil {
		if !u.turn.errored {
			u.addItem(tuiItem{
				Kind:    tuiError,
				Content: err.Error(),
				Time:    time.Now(),
			})
		}
		u.turn = tuiTurn{assistantIndex: -1}
		return
	}
	reply = textutil.Valid(strings.TrimSpace(reply))
	if !u.turn.commandHandled && !u.turn.streamed && reply != "" {
		u.addItem(tuiItem{
			Kind:    tuiAssistant,
			Content: reply,
			Time:    time.Now(),
		})
	}
	u.turn = tuiTurn{assistantIndex: -1}
	u.refreshStatus()
}

func (u *tui) consumeEvents() {
	for {
		select {
		case <-u.ctx.Done():
			return
		case ev, ok := <-u.events:
			if !ok {
				return
			}
			if ev.Source != u.source {
				continue
			}
			u.tv.QueueUpdateDraw(func() {
				u.handleEvent(ev)
			})
		}
	}
}

func (u *tui) handleEvent(ev bus.Event) {
	switch ev.Type {
	case bus.TurnStarted:
		u.busy = true
		u.refreshStatus()
	case bus.TurnChunk:
		u.turn.streamed = true
		u.appendAssistant(ev.Text)
	case bus.ToolCallStarted:
		u.turn.toolCount++
		u.startTool(ev)
	case bus.ToolCallFinished:
		u.finishTool(ev, false)
	case bus.ToolCallFailed:
		u.finishTool(ev, true)
	case bus.CommandHandled:
		u.turn.commandHandled = true
		if strings.TrimSpace(ev.Err) != "" {
			u.turn.errored = true
			u.addItem(tuiItem{Kind: tuiError, Content: ev.Err, Time: ev.Time})
			break
		}
		if u.turn.toolCount == 0 && strings.TrimSpace(ev.Text) != "" {
			u.addItem(tuiItem{Kind: tuiNotice, Content: ev.Text, Time: ev.Time})
		}
	case bus.ServiceNotice:
		u.addItem(tuiItem{Kind: tuiNotice, Content: ev.Text, Time: ev.Time})
	case bus.ModelChanged:
		u.addItem(tuiItem{Kind: tuiNotice, Content: "Model switched to " + ev.Text, Time: ev.Time})
		u.refreshStatus()
	case bus.SessionUpdated:
		u.refreshStatus()
	case bus.TurnFinished:
		u.busy = false
		u.refreshStatus()
	case bus.TurnError:
		u.turn.errored = true
		u.busy = false
		u.addItem(tuiItem{Kind: tuiError, Content: ev.Err, Time: ev.Time})
		u.refreshStatus()
	}
}

func (u *tui) appendAssistant(text string) {
	text = textutil.Valid(text)
	if text == "" {
		return
	}
	if u.turn.assistantIndex < 0 {
		u.turn.assistantIndex = u.addItem(tuiItem{
			Kind:    tuiAssistant,
			Content: text,
			Time:    time.Now(),
		})
		return
	}
	item := u.items[u.turn.assistantIndex]
	item.Content += text
	u.updateItem(u.turn.assistantIndex)
}

func (u *tui) startTool(ev bus.Event) {
	item := tuiItem{
		Kind:    tuiTool,
		Content: summarizeToolAction(ev.Tool, ev.Input),
		Status:  "running",
		Detail:  renderToolDetail(ev, false),
		Time:    eventTime(ev),
	}
	idx := u.addItem(item)
	if ev.ToolCallID != "" {
		u.toolItems[ev.ToolCallID] = idx
	}
}

func (u *tui) finishTool(ev bus.Event, failed bool) {
	idx, ok := u.toolItems[ev.ToolCallID]
	if !ok {
		idx = u.addItem(tuiItem{
			Kind:    tuiTool,
			Content: summarizeToolAction(ev.Tool, ev.Input),
			Time:    eventTime(ev),
		})
	}
	item := u.items[idx]
	if failed {
		item.Status = "failed"
		if strings.TrimSpace(ev.Err) != "" {
			item.Content += " -> " + textutil.Preview(ev.Err, 72)
		}
	} else {
		item.Status = "done"
		summary := summarizeToolOutput(ev.Output)
		if summary != "" {
			item.Content += " -> " + summary
		}
	}
	item.Detail = renderToolDetail(ev, failed)
	u.updateItem(idx)
}

func (u *tui) addItem(item tuiItem) int {
	if item.Time.IsZero() {
		item.Time = time.Now()
	}
	u.items = append(u.items, &item)
	main, secondary := item.listText()
	current := u.transcript.GetCurrentItem()
	last := u.transcript.GetItemCount() - 1
	u.transcript.AddItem(main, secondary, 0, nil)
	idx := len(u.items) - 1
	if current < 0 || current == last {
		u.transcript.SetCurrentItem(idx)
		u.renderInspector(idx)
	}
	return idx
}

func (u *tui) updateItem(idx int) {
	if idx < 0 || idx >= len(u.items) {
		return
	}
	main, secondary := u.items[idx].listText()
	u.transcript.SetItemText(idx, main, secondary)
	if u.transcript.GetCurrentItem() == idx {
		u.renderInspector(idx)
	}
}

func (u *tui) renderInspector(index int) {
	if index < 0 || index >= len(u.items) {
		u.inspector.SetText("Select an item from the transcript to inspect the full message, tool arguments, and output.")
		return
	}
	u.inspector.SetText(u.items[index].inspectorText())
}

func (u *tui) refreshStatus() {
	st := u.state()
	mode := "idle"
	if u.busy {
		mode = "busy"
	}
	approvals := 0
	if u.approver != nil {
		approvals = u.approver.pendingCount()
	}
	fmt.Fprintf(u.status, "mink  runtime %s  model %s  cwd %s  session %s  %s  approvals %d  Tab focus  Ctrl+S send  F2 detail  F1 help",
		st.Runtime, st.Model, st.Cwd, st.Session, mode, approvals)
}

func (u *tui) state() cliState {
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
		Cwd:     shortPath(filepath.Clean(ws)),
		Session: sid,
	}
}

type cliState struct {
	Runtime string
	Model   string
	Cwd     string
	Session string
}

func shortPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~/" + strings.TrimPrefix(path, home+"/")
	}
	return path
}

func eventTime(ev bus.Event) time.Time {
	if ev.Time.IsZero() {
		return time.Now()
	}
	return ev.Time
}

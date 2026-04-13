package mink

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/platform"
)

func (a *App) webMainState(ctx context.Context, src string, state platform.WebState) (platform.WebState, error) {
	state.IndexTitle = "Main Sessions"
	state.IndexAction = "new_session"
	state.IndexActionLabel = "New Session"

	currentID := a.currentMainSession(src)
	if currentID == "" {
		if id, ok := a.sm.CurrentID(src); ok {
			currentID = id
			a.setMainSession(src, id)
		}
	}

	items, err := a.webMainSessionItems(ctx, currentID)
	if err != nil {
		return state, err
	}
	state.IndexGroups = []platform.WebIndexGroup{{Title: "Sessions", Items: items}}
	state.HeaderTitle = "Main Conversation"
	state.HeaderSubtitle = currentID
	state.HeaderMeta = a.webUsageMeta(src)
	state.Messages = a.webMessagesForSource(src)
	state.ContextTitle = "Context"
	state.ContextBlocks = a.webSessionContextBlocks(src)
	state.ComposerLabel = "Message Main Session"
	state.ComposerPlaceholder = "Continue the active main session..."
	if sess, err := a.sm.Current(src); err == nil && sess != nil && strings.TrimSpace(sess.Status()) == "closed" {
		state.ComposerDisabled = true
		state.ComposerPlaceholder = "Session is closed. Start a new session to continue..."
	}
	state.EmptyHint = "Start a new session or pick a resumable conversation."
	return state, nil
}

func (a *App) webInboxState(ctx context.Context, src string, state platform.WebState) (platform.WebState, error) {
	mainItems, err := a.webMainSessionItems(ctx, a.currentMainSession(src))
	if err != nil {
		return state, err
	}
	threads, err := a.webRecentThreads(ctx)
	if err != nil {
		return state, err
	}

	state.IndexTitle = "Inbox"
	state.IndexGroups = []platform.WebIndexGroup{
		{Title: "Main", Items: mainItems},
		{Title: "Threads", Items: a.webThreadIndexItems(threads, a.currentThreadID(src), "threads")},
	}
	state.HeaderTitle = "Inbox"
	state.HeaderSubtitle = "Recent activity across main sessions and team threads"
	state.Cards = a.webInboxCards(ctx, mainItems, threads)
	state.ContextTitle = "How To Use"
	state.ContextBlocks = []platform.WebContextBlock{
		{Title: "Main", Body: "Open or create a resumable main conversation."},
		{Title: "Teams", Body: "Open a team and work directly inside its active thread."},
	}
	state.ComposerLabel = "Select A Conversation"
	state.ComposerPlaceholder = "Inbox is read-only."
	state.ComposerDisabled = true
	state.EmptyHint = "No recent activity yet."
	return state, nil
}

func (a *App) webTeamsState(ctx context.Context, src string, state platform.WebState) (platform.WebState, error) {
	if a.rt == nil {
		state.HeaderTitle = "Teams"
		state.HeaderSubtitle = "Runtime database unavailable"
		state.ComposerDisabled = true
		return state, nil
	}
	teams, err := a.rt.ListTeams(ctx, "")
	if err != nil {
		return state, err
	}
	currentTeam := a.currentTeamID(src)
	currentThread := a.currentThreadID(src)

	teamItems := make([]platform.WebIndexItem, 0, len(teams))
	threadItems := []platform.WebIndexItem{}
	headerTitle := "Teams"
	headerSubtitle := "Select a team"
	contextBlocks := []platform.WebContextBlock{}
	cards := []platform.WebCard{}
	composerPlaceholder := "Select a thread to continue the team conversation..."
	composerDisabled := currentThread == ""

	for _, team := range teams {
		teamItems = append(teamItems, platform.WebIndexItem{
			ID:      team.ID,
			Section: "teams",
			Label:   team.Name,
			Meta:    strings.ToUpper(team.Status),
			Active:  team.ID == currentTeam,
		})
	}

	if currentTeam != "" {
		team, err := a.rt.GetTeam(ctx, currentTeam)
		if err == nil && team.ID != "" {
			headerTitle = team.Name
			headerSubtitle = "Team thread workspace"
			members, _ := a.rt.ListTeamMembers(ctx, team.ID)
			contextBlocks = append(contextBlocks, platform.WebContextBlock{
				Title: "Members",
				Body:  a.webMemberSummary(ctx, members),
			})
			threads, _ := a.rt.ListThreads(ctx, team.ID, "")
			threadItems = a.webThreadIndexItems(a.wrapThreads(team, threads), currentThread, "threads")
			if currentThread == "" {
				for _, thread := range threads {
					info := a.threadInfoFromRecord(ctx, thread)
					cards = append(cards, platform.WebCard{
						Title:    info.Title,
						Subtitle: info.BestAnswer,
						Meta:     info.LatestSummaryAt,
					})
				}
			} else {
				thread, _ := a.rt.GetThread(ctx, currentThread)
				if thread.ID != "" {
					info := a.threadInfoFromRecord(ctx, thread)
					headerTitle = fmt.Sprintf("%s — %s", team.Name, thread.Title)
					headerSubtitle = "Active team thread"
					if strings.TrimSpace(thread.Status) == "closed" {
						headerSubtitle = "Closed team thread"
						composerDisabled = true
						composerPlaceholder = "Thread is closed. Open an active thread to continue..."
					} else {
						composerDisabled = false
						state.IndexAction = "close_thread"
						state.IndexActionLabel = "Close Thread"
					}
					contextBlocks = append(contextBlocks,
						platform.WebContextBlock{Title: "Best Answer", Body: info.BestAnswer},
						platform.WebContextBlock{Title: "Open Blockers", Body: info.OpenBlockers},
						platform.WebContextBlock{Title: "Latest Summary", Body: info.LatestSummary},
					)
					state.Messages = a.webMessagesForSource(src)
				}
			}
		}
	}

	state.IndexTitle = "Teams"
	state.IndexGroups = []platform.WebIndexGroup{
		{Title: "Teams", Items: teamItems},
		{Title: "Threads", Items: threadItems},
	}
	state.HeaderTitle = headerTitle
	state.HeaderSubtitle = headerSubtitle
	state.HeaderMeta = a.webUsageMeta(src)
	state.Cards = cards
	state.ContextTitle = "Context"
	state.ContextBlocks = contextBlocks
	state.ComposerLabel = "Message Team Thread"
	state.ComposerPlaceholder = composerPlaceholder
	state.ComposerDisabled = composerDisabled
	state.EmptyHint = "Create or open a team thread to start working."
	return state, nil
}

func (a *App) webThreadsState(ctx context.Context, src string, state platform.WebState) (platform.WebState, error) {
	threads, err := a.webRecentThreads(ctx)
	if err != nil {
		return state, err
	}
	currentThread := a.currentThreadID(src)
	currentTeam := a.currentTeamID(src)

	state.IndexTitle = "Recent Threads"
	state.IndexGroups = []platform.WebIndexGroup{
		{Title: "Threads", Items: a.webThreadIndexItems(threads, currentThread, "threads")},
	}
	state.HeaderTitle = "Threads"
	state.HeaderSubtitle = "Recent team workstreams"
	state.ComposerLabel = "Message Thread"
	state.ComposerPlaceholder = "Select a thread to continue..."
	state.ComposerDisabled = currentThread == ""
	state.EmptyHint = "No threads yet."

	if currentThread == "" || a.rt == nil {
		return state, nil
	}

	thread, err := a.rt.GetThread(ctx, currentThread)
	if err != nil || thread.ID == "" {
		return state, err
	}
	team, _ := a.rt.GetTeam(ctx, currentTeam)
	info := a.threadInfoFromRecord(ctx, thread)
	state.HeaderTitle = fmt.Sprintf("%s — %s", team.Name, thread.Title)
	state.HeaderSubtitle = "Active thread"
	if strings.TrimSpace(thread.Status) == "closed" {
		state.HeaderSubtitle = "Closed thread"
		state.ComposerDisabled = true
		state.ComposerPlaceholder = "Thread is closed. Open an active thread to continue..."
	} else {
		state.IndexAction = "close_thread"
		state.IndexActionLabel = "Close Thread"
	}
	state.HeaderMeta = a.webUsageMeta(src)
	state.Messages = a.webMessagesForSource(src)
	state.ContextTitle = "Context"
	state.ContextBlocks = []platform.WebContextBlock{
		{Title: "Best Answer", Body: info.BestAnswer},
		{Title: "Open Blockers", Body: info.OpenBlockers},
		{Title: "Latest Summary", Body: info.LatestSummary},
	}
	return state, nil
}


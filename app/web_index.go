package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/mink/platform"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

func (a *App) webMainSessionItems(ctx context.Context, currentID string) ([]platform.WebIndexItem, error) {
	ids, err := a.sm.List()
	if err != nil {
		return nil, err
	}
	excluded, _ := a.webTeamThreadSessionSet(ctx)
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	items := make([]platform.WebIndexItem, 0, len(ids))
	for _, id := range ids {
		if _, skip := excluded[id]; skip {
			continue
		}
		sess, err := a.sm.Get(id)
		if err != nil || sess == nil {
			continue
		}
		if kind := strings.TrimSpace(sess.Kind()); kind != "" && kind != "main" {
			continue
		}
		title := id
		summary := ""
		for _, message := range sess.View().Messages {
			if message.Role == "user" && strings.TrimSpace(message.Content) != "" {
				title = compactLine(message.Content, 28)
				break
			}
		}
		if s := strings.TrimSpace(sess.Summary()); s != "" {
			summary = compactLine(s, 28)
		}
		meta := compactLine(id, 28)
		if status := strings.TrimSpace(sess.Status()); status != "" {
			meta = strings.ToUpper(status)
			if summary != "" {
				meta += " · " + summary
			}
		} else if summary != "" {
			meta = summary
		}
		items = append(items, platform.WebIndexItem{
			ID:      id,
			Section: "main",
			Label:   title,
			Meta:    meta,
			Active:  id == currentID,
		})
	}
	return items, nil
}

func (a *App) webRecentThreads(ctx context.Context) ([]webThreadItem, error) {
	if a.rt == nil {
		return nil, nil
	}
	teams, err := a.rt.ListTeams(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []webThreadItem
	for _, team := range teams {
		threads, err := a.rt.ListThreads(ctx, team.ID, "")
		if err != nil {
			return nil, err
		}
		for _, thread := range threads {
			out = append(out, webThreadItem{Team: team, Thread: thread})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Thread.UpdatedAt > out[j].Thread.UpdatedAt
	})
	return out, nil
}

func (a *App) webThreadIndexItems(threads []webThreadItem, currentThread, section string) []platform.WebIndexItem {
	items := make([]platform.WebIndexItem, 0, len(threads))
	for _, item := range threads {
		meta := item.Team.Name
		if status := strings.TrimSpace(item.Thread.Status); status != "" {
			meta += " · " + strings.ToUpper(status)
		}
		items = append(items, platform.WebIndexItem{
			ID:      item.Thread.ID,
			Section: section,
			Label:   item.Thread.Title,
			Meta:    meta,
			Active:  item.Thread.ID == currentThread,
		})
	}
	return items
}

func (a *App) webInboxCards(ctx context.Context, sessions []platform.WebIndexItem, threads []webThreadItem) []platform.WebCard {
	var cards []platform.WebCard
	for i, item := range sessions {
		if i >= 3 {
			break
		}
		cards = append(cards, platform.WebCard{
			Title:    item.Label,
			Subtitle: "Main conversation",
			Meta:     item.Meta,
		})
	}
	for i, thread := range threads {
		if i >= 3 {
			break
		}
		info := a.threadInfoFromRecord(ctx, thread.Thread)
		cards = append(cards, platform.WebCard{
			Title:    fmt.Sprintf("%s / %s", thread.Team.Name, thread.Thread.Title),
			Subtitle: info.BestAnswer,
			Meta:     info.LatestSummaryAt,
		})
	}
	return cards
}

func (a *App) webMemberSummary(ctx context.Context, members []rtsqlite.TeamMember) string {
	if len(members) == 0 {
		return "No members"
	}
	lines := make([]string, 0, len(members))
	for _, member := range members {
		name := member.AgentID
		if a.rt != nil {
			if ident, err := a.rt.GetAgentIdentity(ctx, member.AgentID); err == nil && ident.DisplayName != "" {
				name = ident.DisplayName
			}
		}
		lines = append(lines, fmt.Sprintf("%s — %s", name, member.RoleName))
	}
	return strings.Join(lines, "\n")
}

func (a *App) webTeamThreadSessionSet(ctx context.Context) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	if a.rt == nil {
		return set, nil
	}
	teams, err := a.rt.ListTeams(ctx, "")
	if err != nil {
		return set, err
	}
	for _, team := range teams {
		threads, err := a.rt.ListThreads(ctx, team.ID, "")
		if err != nil {
			return set, err
		}
		for _, thread := range threads {
			if thread.SessionID != "" {
				set[thread.SessionID] = struct{}{}
			}
		}
	}
	return set, nil
}

func (a *App) wrapThreads(team rtsqlite.Team, threads []rtsqlite.TeamThread) []webThreadItem {
	out := make([]webThreadItem, 0, len(threads))
	for _, thread := range threads {
		out = append(out, webThreadItem{Team: team, Thread: thread})
	}
	return out
}

type webThreadItem struct {
	Team   rtsqlite.Team
	Thread rtsqlite.TeamThread
}

func webTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Format("15:04")
}

func currentWorkspace() string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return "workspace"
	}
	return wd
}

func findWebDist() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "..", "web", "dist")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	if wd, err := os.Getwd(); err == nil {
		dir := filepath.Join(wd, "web", "dist")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

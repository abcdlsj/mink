package collab

import "github.com/abcdlsj/sumi/app"

func Plugin() app.Plugin {
	return func(a *app.App) error {
		m := newManager(a)
		a.RegisterAsyncTurnExecutor(m.executeAsyncTurn)
		a.RegisterTool(spawnTool{m: m})
		a.RegisterTool(delegateTool{m: m})
		a.RegisterTool(delegatePollTool{m: m})
		a.RegisterTool(cancelTool{m: m})
		a.RegisterTool(inviteTool{m: m})
		a.RegisterTool(mentionTool{m: m})
		a.RegisterTool(specialistTool{m: m})
		return nil
	}
}

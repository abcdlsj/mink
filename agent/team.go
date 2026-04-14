package agent

import (
	"sync"

	"github.com/abcdlsj/mink/memory"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

type TeamBinding struct {
	TeamID   string
	ThreadID string
}

type TeamTurn struct {
	TeamID          string
	ThreadID        string
	LeaderAgentID   string
	SpeakerAgentID  string
	RuntimeAgentID  string
	SpeakerProfile  string
	SpeakerRole     string
	SpeakerRoleDesc string
	Round           int
	MaxRounds       int
	TurnPolicy      string
	Goal            string
	Prompt          string
	RuntimeSource   string
}

type TeamHandoff struct {
	SpeakerAgentID string
	Prompt         string
}

type TeamDispatcher struct {
	rt       *rtsqlite.DB
	mem      *memory.Store
	sm       *session.Manager
	mu       sync.RWMutex
	bindings map[string]TeamBinding
	locks    map[string]*sync.Mutex
	pending  map[string]TeamHandoff
}

func NewTeamDispatcher(rt *rtsqlite.DB, mem *memory.Store, sm *session.Manager) *TeamDispatcher {
	return &TeamDispatcher{
		rt:       rt,
		mem:      mem,
		sm:       sm,
		bindings: make(map[string]TeamBinding),
		locks:    make(map[string]*sync.Mutex),
		pending:  make(map[string]TeamHandoff),
	}
}

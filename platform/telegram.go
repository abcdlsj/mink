package platform

import (
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/tool"
	tele "gopkg.in/telebot.v4"
)

const (
	telegramConfirmTimeout = 60 * time.Second
	telegramActiveTTL      = 30 * time.Minute
	telegramStreamMinInt   = 4 * time.Second
	telegramStreamMaxWait  = 5 * time.Second
	telegramStreamMinLen   = 1500
	telegramTypingRefresh  = 10 * time.Second
	telegramMsgLimit       = 3800
	confirmCallbackPrefix  = "mcfm"
	telegramTypingCooldown = 5 * time.Second
)

type confirmState struct {
	ch      chan tool.Approval
	created time.Time
	msgID   int
}

type streamState struct {
	chatID        int64
	buf           strings.Builder
	msgID         int
	progressMsgID int
	progressText  string
	dirty         bool
	ended         bool
	flush         bool
	at            time.Time
}

type inboundState struct {
	msgID    int
	threadID int
}

type assistantOutState struct {
	text      string
	replyToID int
	at        time.Time
}

type Telegram struct {
	token        string
	bus          *bus.Bus
	router       *command.Router
	bot          *tele.Bot
	stop         chan struct{}
	events       chan bus.Msg
	mentionMode  string
	sessionScope string
	agentNames   map[string]string
	agentNamesMu sync.RWMutex

	confirmMu sync.Mutex
	confirms  map[int64]map[string]confirmState

	streamMu sync.Mutex
	streams  map[string]*streamState

	inboundMu sync.RWMutex
	inbound   map[string][]inboundState
	lastIn    map[string]inboundState

	assistMu sync.Mutex
	assist   map[string]assistantOutState

	activeMu    sync.RWMutex
	activeChats map[int64]time.Time

	typingMu   sync.Mutex
	typing     map[int64]chan struct{}
	typingN    map[int64]int
	typingLast map[int64]time.Time
}

type TelegramOptions struct {
	MentionMode  string
	SessionScope string
}

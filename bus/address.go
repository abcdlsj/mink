package bus

import (
	"fmt"
	"regexp"
)

const (
	AddrBroadcast      = "*"
	AddrAgentMain      = "agent:main"
	AddrPlatformCLI    = "platform:cli"
	AddrSystemSession  = "system:session"
	AddrSystemDispatch = "system:dispatcher"
	AddrSystemSup      = "system:supervisor"
)

var addrRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*:[^\s]+$`)

func Agent(id string) string {
	return "agent:" + id
}

func Platform(id string) string {
	return "platform:" + id
}

func Telegram(chatID int64) string {
	return fmt.Sprintf("telegram:%d", chatID)
}

func IsBroadcast(addr string) bool {
	return addr == AddrBroadcast
}

func IsValidAddr(addr string) bool {
	if addr == AddrBroadcast {
		return true
	}
	return addrRe.MatchString(addr)
}

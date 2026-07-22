package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

type PrincipalKind string

const (
	PrincipalHuman PrincipalKind = "human"
	PrincipalAgent PrincipalKind = "agent"
)

type Principal struct {
	Kind PrincipalKind
	ID   string
}

type SpaceKind string

const (
	SpaceDM    SpaceKind = "dm"
	SpaceGroup SpaceKind = "group"
)

type Space struct {
	Kind     SpaceKind
	Archived bool
}

type MembershipChange string

const (
	MembershipAdd    MembershipChange = "add"
	MembershipRemove MembershipChange = "remove"
)

type MessageTargetKind string

const (
	TargetSpace  MessageTargetKind = "space"
	TargetThread MessageTargetKind = "thread"
)

var (
	ErrSpaceArchived                = errors.New("space is archived")
	ErrDMImmutable                  = errors.New("dm membership and lifecycle are immutable")
	ErrDMRequiresDistinctPrincipals = errors.New("dm requires two distinct principals")
	ErrInvalidSpaceName             = errors.New("invalid space name")
	ErrInvalidPrincipal             = errors.New("invalid principal")
	ErrMembershipExists             = errors.New("membership already exists")
	ErrMembershipNotFound           = errors.New("membership not found")
	ErrLastActiveHumanMember        = errors.New("cannot remove last active human member")
	ErrInvalidMessageTarget         = errors.New("invalid message target")
	ErrInvalidMessageBody           = errors.New("invalid message body")
	ErrInvalidSpace                 = errors.New("invalid space")
)

func CanonicalDMKey(first, second Principal) (string, error) {
	if first.Kind == second.Kind && first.ID == second.ID {
		return "", ErrDMRequiresDistinctPrincipals
	}
	parts := []string{string(first.Kind) + ":" + first.ID, string(second.Kind) + ":" + second.ID}
	sort.Strings(parts)
	digest := sha256.Sum256([]byte(parts[0] + "\x00" + parts[1]))
	return hex.EncodeToString(digest[:]), nil
}

func ValidatePrincipal(principal Principal) error {
	if (principal.Kind != PrincipalHuman && principal.Kind != PrincipalAgent) || principal.ID == "" {
		return ErrInvalidPrincipal
	}
	return nil
}

func ValidateSpaceName(name string) error {
	if !utf8.ValidString(name) {
		return ErrInvalidSpaceName
	}
	count := utf8.RuneCountInString(name)
	if count < 1 || count > 100 || strings.TrimSpace(name) == "" {
		return ErrInvalidSpaceName
	}
	return nil
}

func ValidateMessageBody(body string) error {
	if !utf8.ValidString(body) {
		return ErrInvalidMessageBody
	}
	count := utf8.RuneCountInString(body)
	if count < 1 || count > 400_000 {
		return ErrInvalidMessageBody
	}
	return nil
}

func ValidateMessageTarget(kind MessageTargetKind) error {
	if kind != TargetSpace && kind != TargetThread {
		return ErrInvalidMessageTarget
	}
	return nil
}

func (space Space) ValidateMembershipChange(change MembershipChange, member Principal, exists bool, remainingActiveHumanMembers int) error {
	if err := space.ValidateMembershipMutation(); err != nil {
		return err
	}
	if err := ValidatePrincipal(member); err != nil {
		return err
	}
	if change == MembershipAdd {
		if exists {
			return ErrMembershipExists
		}
		return nil
	}
	if change != MembershipRemove {
		return ErrInvalidPrincipal
	}
	if !exists {
		return ErrMembershipNotFound
	}
	if member.Kind == PrincipalHuman && remainingActiveHumanMembers == 0 {
		return ErrLastActiveHumanMember
	}
	return nil
}

func (space Space) ValidateMembershipMutation() error {
	if space.Kind != SpaceDM && space.Kind != SpaceGroup {
		return ErrInvalidSpace
	}
	if space.Kind == SpaceDM {
		return ErrDMImmutable
	}
	if space.Archived {
		return ErrSpaceArchived
	}
	return nil
}

func (space Space) ValidateArchiveChange() error {
	if space.Kind != SpaceDM && space.Kind != SpaceGroup {
		return ErrInvalidSpace
	}
	if space.Kind == SpaceDM {
		return ErrDMImmutable
	}
	return nil
}

func (space Space) ValidateMessageSend() error {
	if space.Kind != SpaceDM && space.Kind != SpaceGroup {
		return ErrInvalidSpace
	}
	if space.Archived {
		return ErrSpaceArchived
	}
	return nil
}

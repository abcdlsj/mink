package domain

import (
	"errors"
	"testing"
)

func TestCanonicalDMKeyIsOrderIndependent(t *testing.T) {
	first := Principal{Kind: PrincipalHuman, ID: "human"}
	second := Principal{Kind: PrincipalAgent, ID: "agent"}
	left, err := CanonicalDMKey(first, second)
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalDMKey(second, first)
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("keys differ: %q != %q", left, right)
	}
	if _, err := CanonicalDMKey(first, first); !errors.Is(err, ErrDMRequiresDistinctPrincipals) {
		t.Fatalf("same principal error = %v", err)
	}
}

func TestSpaceMembershipChange(t *testing.T) {
	group := Space{Kind: SpaceGroup}
	human := Principal{Kind: PrincipalHuman, ID: "human"}
	cases := []struct {
		name      string
		space     Space
		change    MembershipChange
		member    Principal
		exists    bool
		remaining int
		want      error
	}{
		{name: "add new", space: group, change: MembershipAdd, member: human},
		{name: "duplicate add", space: group, change: MembershipAdd, member: human, exists: true, want: ErrMembershipExists},
		{name: "remove missing", space: group, change: MembershipRemove, member: human, want: ErrMembershipNotFound},
		{name: "remove last human", space: group, change: MembershipRemove, member: human, exists: true, remaining: 0, want: ErrLastActiveHumanMember},
		{name: "remove inactive human", space: group, change: MembershipRemove, member: human, exists: true, remaining: 1},
		{name: "dm immutable", space: Space{Kind: SpaceDM}, change: MembershipAdd, member: human, want: ErrDMImmutable},
		{name: "archived", space: Space{Kind: SpaceGroup, Archived: true}, change: MembershipAdd, member: human, want: ErrSpaceArchived},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := test.space.ValidateMembershipChange(test.change, test.member, test.exists, test.remaining)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestSpaceArchiveChange(t *testing.T) {
	if err := (Space{Kind: SpaceDM}).ValidateArchiveChange(); !errors.Is(err, ErrDMImmutable) {
		t.Fatalf("dm archive error = %v", err)
	}
	if err := (Space{Kind: SpaceGroup}).ValidateArchiveChange(); err != nil {
		t.Fatalf("group archive error = %v", err)
	}
}

func TestSpaceMessageSend(t *testing.T) {
	tests := []struct {
		name  string
		space Space
		want  error
	}{
		{name: "active group", space: Space{Kind: SpaceGroup}},
		{name: "active dm", space: Space{Kind: SpaceDM}},
		{name: "archived group", space: Space{Kind: SpaceGroup, Archived: true}, want: ErrSpaceArchived},
		{name: "invalid kind", space: Space{Kind: "invalid"}, want: ErrInvalidSpace},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.space.ValidateMessageSend(); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

package command

type sessionResetter interface {
	InvalidateSource(string)
}

type sessionTeamResetter interface {
	UnbindTeamSource(string)
}

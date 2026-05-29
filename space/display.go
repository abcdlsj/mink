package space

type DisplayResolver interface {
	Display(id string) string
}

type displayResolverFunc func(string) string

func (f displayResolverFunc) Display(id string) string { return f(id) }

func DisplayResolverFunc(f func(string) string) DisplayResolver {
	return displayResolverFunc(f)
}

func MessageAuthorDisplay(sp *Space, m Message, resolver DisplayResolver) string {
	id := m.AuthorID
	if id == "" {
		return ""
	}
	if m.AuthorKind == ParticipantUser {
		if sp != nil {
			for _, p := range sp.Participants {
				if p.ID == id && p.Display != "" {
					return p.Display
				}
			}
		}
		return id
	}
	if resolver != nil {
		if d := resolver.Display(id); d != "" {
			return d
		}
	}
	if sp != nil {
		for _, p := range sp.Participants {
			if p.ID == id && p.Display != "" {
				return p.Display
			}
		}
	}
	return id
}

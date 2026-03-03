package llm

type Sel struct{ p, c Provider }

func NewSel(p, c Provider) *Sel { return &Sel{p, c} }

func (s *Sel) P(m string) Provider {
	if m == "cheap" {
		return s.c
	}
	return s.p
}

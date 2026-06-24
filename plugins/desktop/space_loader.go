package desktop

import (
	"strings"

	"github.com/abcdlsj/sumi/space"
)

type SpaceLoader struct {
	spaces *space.Manager
}

func (b *Backend) spaceLoader() SpaceLoader {
	if b == nil || b.app == nil {
		return SpaceLoader{}
	}
	return SpaceLoader{spaces: b.app.Spaces()}
}

func (l SpaceLoader) Load(id string) (*space.Space, error) {
	if l.spaces == nil {
		return nil, nil
	}
	return l.spaces.LoadSpace(strings.TrimSpace(id))
}

func (l SpaceLoader) LoadTyped(id string, kind space.Kind) (*space.Space, error) {
	sp, err := l.Load(id)
	if err != nil || sp == nil {
		return sp, err
	}
	if sp.Kind != kind {
		return nil, nil
	}
	return sp, nil
}

func (l SpaceLoader) LoadThreadable(id string) (*space.Space, error) {
	sp, err := l.Load(id)
	if err != nil || sp == nil {
		return sp, err
	}
	if !threadKind(sp.Kind) {
		return nil, nil
	}
	return sp, nil
}

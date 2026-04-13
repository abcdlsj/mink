package session

type Store interface {
	Load(id string) (*Snapshot, error)
	Save(id string, snap *Snapshot) error
	Delete(id string) error
	List() ([]string, error)
}

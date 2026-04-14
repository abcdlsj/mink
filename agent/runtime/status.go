package runtime

type Status int

const (
	Idle Status = iota
	Running
	Stopped
)

func (s Status) String() string {
	switch s {
	case Idle:
		return "idle"
	case Running:
		return "running"
	case Stopped:
		return "stopped"
	default:
		return "unknown"
	}
}

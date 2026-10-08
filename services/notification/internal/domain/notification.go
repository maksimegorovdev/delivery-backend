package domain

type Channel string

const (
	ChannelLog Channel = "log"
)

type Notification struct {
	ID        string
	EventID   string
	EventType string
	Channel   Channel
	Payload   []byte
	Attempts  int
}

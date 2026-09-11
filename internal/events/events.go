package events

import "time"

type Event struct {
	ID       int64
	Name     string
	Data     time.Time
	Location string
}

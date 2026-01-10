package types

import "time"

// Task represents a background task that can be triggered
type Task struct {
	LastRanAt   *time.Time
	ID          string
	Name        string
	Description string
}

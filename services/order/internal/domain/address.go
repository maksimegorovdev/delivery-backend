package domain

import "time"

type Address struct {
	ID        string
	UserID    string
	Address   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

package domain

import "time"

type Product struct {
	ID        string
	Name      string
	Price     int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

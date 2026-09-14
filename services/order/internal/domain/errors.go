package domain

import "errors"

var (
	ErrOrderNoItems    = errors.New("order: no items")
	ErrProductNotFound = errors.New("order: product not found")
)

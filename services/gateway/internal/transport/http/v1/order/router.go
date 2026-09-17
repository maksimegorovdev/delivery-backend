package order

import "github.com/go-chi/chi/v5"

func Routes(r chi.Router, handler *OrderHandler) {
	r.Post("/orders", handler.CreateOrder)
}

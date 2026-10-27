package order

import "github.com/go-chi/chi/v5"

func Routes(r chi.Router, handler *Handler) {
	r.Post("/orders", handler.CreateOrder)
}

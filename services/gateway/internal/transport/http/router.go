package http

import (
	"github.com/go-chi/chi/v5"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http/swagger"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http/v1/order"
)

type RouterDeps struct {
	Router       chi.Router
	OrderHandler *order.OrderHandler
}

// NewRouter initializes all routes
//
//	@title			Swagger Go Delivery Microservices API
//	@version		1.0
//	@description	These are microservices for delivery on Go
//	@BasePath		/api
func NewRouter(deps RouterDeps) {
	deps.Router.Route("/api", func(r chi.Router) {
		r.Route("/v1", func(r chi.Router) {
			order.Routes(r, deps.OrderHandler)
		})
	})
	swagger.Routes(deps.Router)
}

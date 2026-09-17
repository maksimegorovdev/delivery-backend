package swagger

import (
	"github.com/go-chi/chi/v5"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/maksimegorovdev/delivery-backend/services/gateway/docs"
)

func Routes(r chi.Router) {
	r.Get("/swagger/*", httpSwagger.Handler())
}

package order

import (
	"context"
	"net/http"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/http/bind"
	"github.com/maksimegorovdev/delivery-backend/platform/http/response"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/service"
)

type Service interface {
	CreateOrder(ctx context.Context, input service.CreateOrderInput) (domain.Order, error)
}

type Handler struct {
	uc Service
}

func NewOrderHandler(uc Service) *Handler {
	return &Handler{uc: uc}
}

// CreateOrder create a new order
//
//	@Summary		Create an order
//	@Description	Creates a new order with the provided details
//	@Tags			orders
//	@Accept			json
//	@Produce		json
//	@Param			order	body		CreateOrderRequestDTO	true	"Order data"
//	@Success		201		{object}	response.Envelope{data=ResponseDTO}
//	@Failure		default	{object}	response.Envelope{error=response.ErrorBody}
//	@Router			/v1/orders [post]
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	dto, err := bind.JSON[CreateOrderRequestDTO](r)
	if err != nil {
		response.Fail(w, r, err)
		return
	}

	input := toCreateOrderInput(appctx.GetUserID(r.Context()), dto)
	order, err := h.uc.CreateOrder(r.Context(), input)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.Created(w, r, toResponseDTO(order))
}

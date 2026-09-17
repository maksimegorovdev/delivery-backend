package order

import (
	"context"
	"net/http"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/http/bind"
	"github.com/maksimegorovdev/delivery-backend/platform/http/response"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/usecase"
)

type OrderUsecase interface {
	CreateOrder(ctx context.Context, input usecase.CreateOrderInput) (domain.Order, error)
}

type OrderHandler struct {
	uc OrderUsecase
}

func NewOrderHandler(uc OrderUsecase) *OrderHandler {
	return &OrderHandler{uc: uc}
}

// CreateOrder create a new order
//
//	@Summary		Create an order
//	@Description	Creates a new order with the provided details
//	@Tags			orders
//	@Accept			json
//	@Produce		json
//	@Param			order	body		CreateOrderRequestDTO	true	"Order data"
//	@Success		201		{object}	response.Envelope{data=OrderResponseDTO}
//	@Failure		default	{object}	response.Envelope{error=response.ErrorBody}
//	@Router			/v1/orders [post]
func (h *OrderHandler) CreateOrder(w http.ResponseWriter, r *http.Request) {
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
	response.Created(w, r, toOrderResponseDTO(order))
}

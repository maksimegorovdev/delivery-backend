package order_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
	"github.com/maksimegorovdev/delivery-backend/platform/http/response"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/service"
	httptransport "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http/v1/order"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http/v1/order/mocks"
)

const (
	ordersURL     = "/api/v1/orders"
	defaultUserID = "default-user"
)

// envelope повторяет response.Envelope, но с типизированным Data.
type envelope struct {
	Meta  response.Meta       `json:"meta"`
	Data  *order.ResponseDTO  `json:"data"`
	Error *response.ErrorBody `json:"error"`
}

// newRouter собирает роутер так же, как app: те же middleware и httptransport.NewRouter.
func newRouter(t *testing.T) (http.Handler, *mocks.MockService) {
	t.Helper()

	svc := mocks.NewMockService(t)
	r := chi.NewRouter()
	r.Use(
		middleware.RequestID,
		middleware.Recover,
		middleware.FakeAuth(defaultUserID),
	)
	httptransport.NewRouter(httptransport.RouterDeps{
		Router:       r,
		OrderHandler: order.NewOrderHandler(svc),
	})

	return r, svc
}

func post(t *testing.T, h http.Handler, body string, headers ...string) (*httptest.ResponseRecorder, envelope) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, ordersURL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	var env envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), "body: %s", rec.Body.String())
	return rec, env
}

func validBody() string {
	return `{"address_id":"` + validUUID + `","items":[` +
		`{"product_id":"` + validProductID + `","quantity":2},` +
		`{"product_id":"` + validUUID + `","quantity":1}]}`
}

func wantInput(userID string) service.CreateOrderInput {
	return service.CreateOrderInput{
		UserID:    userID,
		AddressID: validUUID,
		Items: []service.CreateOrderItemInput{
			{ProductID: validProductID, Quantity: 2},
			{ProductID: validUUID, Quantity: 1},
		},
	}
}

func TestHandler_CreateOrder_Success(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)
	updated := time.Date(2026, time.October, 30, 12, 5, 0, 0, time.UTC)
	domainOrder := domain.Order{
		ID:     "order-1",
		UserID: "user-1",
		Status: domain.OrderStatusCreated,
		Items: []domain.OrderItem{
			{ID: "item-1", ProductID: validProductID, ProductName: "Milk", Quantity: 2, UnitPrice: 100},
			{ID: "item-2", ProductID: validUUID, ProductName: "Bread", Quantity: 1, UnitPrice: 50},
		},
		TotalAmount:     250,
		DeliveryAddress: "Lenina 1",
		CreatedAt:       created,
		UpdatedAt:       updated,
	}

	t.Run("returns 201 with mapped order and passes the mapped input to the service", func(t *testing.T) {
		t.Parallel()

		h, svc := newRouter(t)
		svc.EXPECT().CreateOrder(mock.Anything, wantInput("user-1")).Return(domainOrder, nil).Once()

		rec, env := post(t, h, validBody(), middleware.UserIDHeader, "user-1", middleware.RequestIDHeader, "req-1")

		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
		assert.Nil(t, env.Error)
		assert.Equal(t, "req-1", env.Meta.RequestID)
		assert.Equal(t, &order.ResponseDTO{
			ID:     "order-1",
			UserID: "user-1",
			Status: "created",
			Items: []order.ItemResponseDTO{
				{ID: "item-1", ProductID: validProductID, ProductName: "Milk", Quantity: 2, UnitPrice: 100},
				{ID: "item-2", ProductID: validUUID, ProductName: "Bread", Quantity: 1, UnitPrice: 50},
			},
			TotalAmount:     250,
			DeliveryAddress: "Lenina 1",
			CreatedAt:       created,
			UpdatedAt:       updated,
		}, env.Data)
	})

	t.Run("uses the default user when the user header is missing", func(t *testing.T) {
		t.Parallel()

		h, svc := newRouter(t)
		svc.EXPECT().CreateOrder(mock.Anything, wantInput(defaultUserID)).Return(domainOrder, nil).Once()

		rec, _ := post(t, h, validBody())

		assert.Equal(t, http.StatusCreated, rec.Code)
	})

	t.Run("serializes an order without items as an empty array and uses snake_case fields", func(t *testing.T) {
		t.Parallel()

		h, svc := newRouter(t)
		svc.EXPECT().CreateOrder(mock.Anything, mock.Anything).
			Return(domain.Order{ID: "order-1", Status: domain.OrderStatusPaid}, nil).Once()

		rec, _ := post(t, h, validBody())

		require.Equal(t, http.StatusCreated, rec.Code)
		var raw struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		assert.JSONEq(t, `[]`, string(raw.Data["items"]))
		assert.JSONEq(t, `"paid"`, string(raw.Data["status"]))
		for _, field := range []string{"id", "user_id", "status", "items", "total_amount", "delivery_address", "created_at", "updated_at"} {
			assert.Contains(t, raw.Data, field)
		}
	})
}

func TestHandler_CreateOrder_InvalidBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "not json", body: `not a json`},
		{name: "empty body", body: ``},
		{name: "truncated json", body: `{"address_id":`},
		{name: "wrong field type", body: `{"address_id":123,"items":[]}`},
		{name: "items is not an array", body: `{"address_id":"` + validUUID + `","items":"x"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, _ := newRouter(t) // сервис без ожиданий: любой вызов роняет тест

			rec, env := post(t, h, tt.body)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Nil(t, env.Data)
			require.NotNil(t, env.Error)
			assert.Equal(t, apperr.CodeInvalidArgument.String(), env.Error.Code)
			assert.Equal(t, apperr.ReasonInvalidRequestBody, env.Error.Reason)
		})
	}
}

func TestHandler_CreateOrder_ValidationFailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantFields []string
	}{
		{name: "empty object", body: `{}`, wantFields: []string{"address_id", "items"}},
		{
			name:       "address is not a uuid",
			body:       `{"address_id":"a-1","items":[{"product_id":"` + validProductID + `","quantity":1}]}`,
			wantFields: []string{"address_id"},
		},
		{
			name:       "empty items",
			body:       `{"address_id":"` + validUUID + `","items":[]}`,
			wantFields: []string{"items"},
		},
		{
			name:       "item with zero quantity",
			body:       `{"address_id":"` + validUUID + `","items":[{"product_id":"` + validProductID + `","quantity":0}]}`,
			wantFields: []string{"items.0.quantity"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, _ := newRouter(t)

			rec, env := post(t, h, tt.body)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			require.NotNil(t, env.Error)
			assert.Equal(t, apperr.CodeInvalidArgument.String(), env.Error.Code)
			var gotFields []string
			for _, v := range env.Error.Violations {
				gotFields = append(gotFields, v.Field)
				assert.NotEmpty(t, v.Message)
			}
			assert.Equal(t, tt.wantFields, gotFields)
		})
	}
}

func TestHandler_CreateOrder_ServiceError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   apperr.Code
	}{
		{"not found", apperr.NotFound(), http.StatusNotFound, apperr.CodeNotFound},
		{"already exists", apperr.AlreadyExists(), http.StatusConflict, apperr.CodeAlreadyExists},
		{"invalid argument", apperr.InvalidArgument(), http.StatusBadRequest, apperr.CodeInvalidArgument},
		{"unavailable", apperr.Unavailable(), http.StatusServiceUnavailable, apperr.CodeUnavailable},
		{
			"wrapped apperr keeps its code",
			apperr.NotFound().Wrap(errors.New("product p-1")),
			http.StatusNotFound,
			apperr.CodeNotFound,
		},
		{
			"plain error becomes internal",
			errors.New("db password is hunter2"),
			http.StatusInternalServerError,
			apperr.CodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, svc := newRouter(t)
			svc.EXPECT().CreateOrder(mock.Anything, mock.Anything).Return(domain.Order{}, tt.err).Once()

			rec, env := post(t, h, validBody())

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Nil(t, env.Data)
			require.NotNil(t, env.Error)
			assert.Equal(t, tt.wantCode.String(), env.Error.Code)
			assert.NotContains(t, rec.Body.String(), "hunter2", "internal error text must not leak to the client")
		})
	}
}

func TestRouter_OrdersRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"get is not allowed", http.MethodGet, ordersURL, http.StatusMethodNotAllowed},
		{"put is not allowed", http.MethodPut, ordersURL, http.StatusMethodNotAllowed},
		{"route without the api prefix is not found", http.MethodPost, "/v1/orders", http.StatusNotFound},
		{"unknown route is not found", http.MethodPost, "/api/v1/unknown", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, _ := newRouter(t)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(validBody()))
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

//go:build integration

package grpc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	productv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/product/v1"
)

func TestProductStack_GetProducts(t *testing.T) {
	t.Parallel()

	t.Run("returns stored product", func(t *testing.T) {
		t.Parallel()

		client := newProductClient(t)

		resp, err := client.GetProducts(t.Context(), &productv1.GetProductsRequest{Ids: []string{stackProductID}})

		require.NoError(t, err)
		require.Len(t, resp.GetProducts(), 1)
		product := resp.GetProducts()[0]
		assert.Equal(t, stackProductID, product.GetId())
		assert.Equal(t, stackProductName, product.GetName())
		assert.Equal(t, stackProductPrice, product.GetPrice())
		assert.Equal(t, stackCreatedAt(), product.GetCreatedAt().AsTime())
		assert.Equal(t, stackUpdatedAt(), product.GetUpdatedAt().AsTime())
	})

	t.Run("returns several products", func(t *testing.T) {
		t.Parallel()

		client := newProductClient(t)

		resp, err := client.GetProducts(t.Context(), &productv1.GetProductsRequest{
			Ids: []string{stackProductID, stackOtherProductID},
		})

		require.NoError(t, err)
		got := make([]string, 0, len(resp.GetProducts()))
		for _, p := range resp.GetProducts() {
			got = append(got, p.GetId())
		}
		assert.ElementsMatch(t, []string{stackProductID, stackOtherProductID}, got)
	})

	t.Run("missing ids are skipped without error", func(t *testing.T) {
		t.Parallel()

		client := newProductClient(t)

		resp, err := client.GetProducts(t.Context(), &productv1.GetProductsRequest{
			Ids: []string{stackProductID, stackMissingID},
		})

		require.NoError(t, err)
		require.Len(t, resp.GetProducts(), 1)
		assert.Equal(t, stackProductID, resp.GetProducts()[0].GetId())
	})

	t.Run("only missing ids return empty response", func(t *testing.T) {
		t.Parallel()

		client := newProductClient(t)

		resp, err := client.GetProducts(t.Context(), &productv1.GetProductsRequest{Ids: []string{stackMissingID}})

		require.NoError(t, err)
		assert.Empty(t, resp.GetProducts())
	})
}

func TestProductStack_GetProducts_InvalidRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ids  []string
	}{
		{name: "empty list", ids: nil},
		{name: "not a uuid", ids: []string{"not-a-uuid"}},
		{name: "empty id", ids: []string{""}},
		{name: "duplicate ids", ids: []string{stackProductID, stackProductID}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newProductClient(t)

			resp, err := client.GetProducts(t.Context(), &productv1.GetProductsRequest{Ids: tt.ids})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			// Сообщение валидатора, а не маппинга ошибки БД: до репозитория запрос не дошёл.
			assert.Equal(t, apperr.ReasonMessageValidationFailed, status.Convert(err).Message())
			assert.Nil(t, resp)
		})
	}
}

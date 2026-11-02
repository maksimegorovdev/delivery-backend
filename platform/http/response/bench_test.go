package response_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/http/response"
)

type discardWriter struct {
	header http.Header
}

func (w *discardWriter) Header() http.Header       { return w.header }
func (*discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (*discardWriter) WriteHeader(int)             {}

type benchItem struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

type benchOrder struct {
	ID     string      `json:"id"`
	Status string      `json:"status"`
	Items  []benchItem `json:"items"`
}

func newBenchOrder(items int) benchOrder {
	o := benchOrder{ID: "01a0d1fa-a4e8-75a1-802d-d6e3c642b0f3", Status: "pending", Items: make([]benchItem, items)}
	for i := range o.Items {
		o.Items[i] = benchItem{ProductID: "01a0d1fa-a4e8-75a1-802d-d6e3c642b0f4", Quantity: 3}
	}
	return o
}

func benchRequest(b *testing.B) *http.Request {
	b.Helper()

	req := httptest.NewRequestWithContext(b.Context(), http.MethodPost, "/orders", nil)
	ctx := appctx.WithRequestID(appctx.WithErrBox(req.Context()), "bench-request-id")
	return req.WithContext(ctx)
}

func BenchmarkResponse_Created(b *testing.B) {
	for _, items := range []int{1, 10} {
		b.Run(itemsName(items), func(b *testing.B) {
			req := benchRequest(b)
			data := newBenchOrder(items)
			w := &discardWriter{header: make(http.Header)}

			b.ReportAllocs()
			for b.Loop() {
				response.Created(w, req, data)
			}
		})
	}
}

func BenchmarkResponse_Fail(b *testing.B) {
	req := benchRequest(b)
	err := apperr.ValidationFailed(apperr.Violation{Field: "items", Message: "must not be empty"})
	w := &discardWriter{header: make(http.Header)}

	b.ReportAllocs()
	for b.Loop() {
		response.Fail(w, req, err)
	}
}

func itemsName(n int) string {
	if n == 1 {
		return "items=1"
	}
	return "items=10"
}

package interceptors_test

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
)

// nopWriter — приёмник логов: JSON форматируется как в бою, но не пишется (slog.DiscardHandler пропустил бы форматирование).
type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// BenchmarkInterceptors_Chain — цепочка Error → Logger → Validation в порядке сервисов
// (services/*/internal/app/app.go) с пустым handler, без сервера и сети. У HealthCheckRequest нет правил
// protovalidate: Validation измеряется как накладные расходы интерцептора, а не стоимость правил контракта.
func BenchmarkInterceptors_Chain(b *testing.B) {
	validator, err := protovalidate.New()
	if err != nil {
		b.Fatal(err)
	}
	log := slog.New(slog.NewJSONHandler(nopWriter{}, nil))

	unary := []grpc.UnaryServerInterceptor{
		interceptors.Error(),
		interceptors.Logger(log),
		interceptors.Validation(validator),
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
	req := &grpc_health_v1.HealthCheckRequest{Service: "bench"}

	cases := []struct {
		name     string
		handler  grpc.UnaryHandler
		wantCode codes.Code
	}{
		{"success", func(context.Context, any) (any, error) {
			return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
		}, codes.OK},
		{"failure", func(context.Context, any) (any, error) {
			return nil, apperr.NotFound()
		}, codes.NotFound},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			invoke := buildChain(unary, info, tc.handler)
			// Цепочка, оборвавшая вызов раньше handler, измерялась бы молча: путь проверяется до замера.
			if _, callErr := invoke(b.Context(), req); status.Code(callErr) != tc.wantCode {
				b.Fatalf("chain returned %v, want code %v", callErr, tc.wantCode)
			}

			b.ReportAllocs()
			for b.Loop() {
				_, _ = invoke(b.Context(), req)
			}
		})
	}
}

// buildChain оборачивает handler интерцепторами так же, как grpc.ChainUnaryInterceptor.
func buildChain(
	unary []grpc.UnaryServerInterceptor,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) grpc.UnaryHandler {
	for _, interceptor := range slices.Backward(unary) {
		next := handler
		handler = func(ctx context.Context, req any) (any, error) {
			return interceptor(ctx, req, info, next)
		}
	}
	return handler
}

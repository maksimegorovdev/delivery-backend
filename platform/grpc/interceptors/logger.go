package interceptors

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
)

func Logger(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}

		e := apperr.From(err)
		log.LogAttrs(
			ctx,
			e.Code.Level(),
			"grpc request failed",
			slog.GroupAttrs(
				"grpc",
				slog.String("code", e.Code.GRPC().String()),
				slog.String("method", info.FullMethod),
				slog.Duration("duration_ms", time.Since(start)),
			),
			logger.Err(err),
		)
		return resp, err
	}
}

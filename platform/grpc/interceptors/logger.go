package interceptors

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

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

		if err != nil {
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

		log.LogAttrs(
			ctx,
			slog.LevelInfo,
			"grpc request handled",
			slog.GroupAttrs(
				"grpc",
				slog.String("code", codes.OK.String()),
				slog.String("method", info.FullMethod),
				slog.Duration("duration_ms", time.Since(start)),
			),
			logger.Err(err),
		)
		return resp, nil
	}
}

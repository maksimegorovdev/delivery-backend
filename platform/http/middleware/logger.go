package middleware

import (
	"log/slog"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
)

func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			ctx := appctx.WithErrBox(r.Context())

			next.ServeHTTP(ww, r.WithContext(ctx))

			if err := appctx.GetErr(ctx); err != nil {
				e := apperr.From(err)
				log.LogAttrs(
					ctx,
					e.Code.Level(),
					"http request failed",
					slog.GroupAttrs(
						"http",
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.Int("status_code", ww.Status()),
						slog.Duration("duration_ms", time.Since(start)),
					),
					logger.Err(err),
				)
				return
			}

			log.LogAttrs(
				ctx,
				slog.LevelInfo,
				"http request handled",
				slog.GroupAttrs(
					"http",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status_code", ww.Status()),
					slog.Duration("duration_ms", time.Since(start)),
				),
			)
		})
	}
}

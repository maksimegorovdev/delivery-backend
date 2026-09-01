package pgerr

import (
	"context"
	"errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

func Map(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apperr.As(err); ok {
		return err
	}

	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return apperr.Canceled().Wrap(err)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return apperr.DeadlineExceeded().Wrap(err)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound().Wrap(err)
	}

	var pg *pgconn.PgError
	if !errors.As(err, &pg) || len(pg.Code) < 2 {
		return apperr.Internal().Wrap(err)
	}

	switch pg.Code {
	case pgerrcode.UniqueViolation, pgerrcode.ExclusionViolation:
		return apperr.AlreadyExists().Wrap(err)
	case pgerrcode.QueryCanceled:
		return apperr.DeadlineExceeded().Wrap(err)
	}

	switch pg.Code[:2] {
	case "22", "23":
		return apperr.InvalidArgument().Wrap(err)
	case "08", "40", "53", "55", "57":
		return apperr.Unavailable().Wrap(err)
	}

	return apperr.Internal().Wrap(err)
}

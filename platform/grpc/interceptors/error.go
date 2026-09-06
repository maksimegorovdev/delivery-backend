package interceptors

import (
	"context"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr/grpcerr"
)

func Error() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}

		e := apperr.From(err)

		st := status.New(e.Code.GRPC(), e.Public())

		errInfo := &errdetails.ErrorInfo{
			Reason: e.Code.String(),
			Domain: grpcerr.Domain,
		}
		if e.Reason != "" {
			errInfo.Metadata = map[string]string{
				"reason": e.Reason,
			}
		}

		details := []protoadapt.MessageV1{errInfo}
		if len(e.Violations) > 0 {
			br := &errdetails.BadRequest{}
			for _, v := range e.Violations {
				br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{
					Field:       v.Field,
					Description: v.Message,
				})
			}
			details = append(details, br)
		}
		if enriched, derr := st.WithDetails(details...); derr == nil {
			st = enriched
		}
		return nil, st.Err()
	}
}

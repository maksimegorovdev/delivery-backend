package interceptors

import (
	"context"
	"errors"

	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

func Validation(validator protovalidate.Validator) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		msg, ok := req.(proto.Message)
		if !ok {
			return handler(ctx, req)
		}

		if err := validator.Validate(msg); err != nil {
			if validationErr, ok := errors.AsType[*protovalidate.ValidationError](err); ok {
				violations := make([]apperr.Violation, 0, len(validationErr.Violations))
				for _, violation := range validationErr.Violations {
					violations = append(violations, apperr.Violation{
						Field:   protovalidate.FieldPathString(violation.Proto.GetField()),
						Message: violation.Proto.GetMessage(),
					})
				}
				return nil, apperr.ValidationFailed(violations...)
			}
			return nil, apperr.Internal().Wrap(err)
		}

		return handler(ctx, msg)
	}
}

package grpcerr

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

const Domain = "apperr"

func Map(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return apperr.Internal().Wrap(err)
	}

	code := apperr.CodeFromGRPC(st.Code())
	var reason string
	var violations []apperr.Violation

	for _, d := range st.Details() {
		switch t := d.(type) {
		case *errdetails.ErrorInfo:
			if t.GetDomain() == Domain {
				if r := t.GetReason(); r != "" {
					code = apperr.Code(r)
				}
				reason = t.GetMetadata()["reason"]
			}
		case *errdetails.BadRequest:
			for _, fv := range t.GetFieldViolations() {
				violations = append(violations, apperr.Violation{
					Field:   fv.GetField(),
					Message: fv.GetDescription(),
				})
			}
		}
	}

	out := apperr.New(code).WithMessage(st.Message()).Wrap(err)
	if reason != "" {
		out.WithReason(reason)
	}
	if len(violations) > 0 {
		out.WithViolations(violations...)
	}
	return out
}

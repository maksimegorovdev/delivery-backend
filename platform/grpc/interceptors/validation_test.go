package interceptors_test

import (
	"errors"
	"testing"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
)

// stubValidator возвращает заранее заданный результат валидации.
type stubValidator struct {
	err error
}

func (v stubValidator) Validate(proto.Message, ...protovalidate.ValidationOption) error {
	return v.err
}

func fieldViolation(field, message string) *protovalidate.Violation {
	return &protovalidate.Violation{
		Proto: &validate.Violation{
			Field:   &validate.FieldPath{Elements: []*validate.FieldPathElement{{FieldName: new(field)}}},
			Message: new(message),
		},
	}
}

func TestValidation_ValidRequestReachesHandler(t *testing.T) {
	t.Parallel()

	client, svc := newHealthClient(t, okCheck, interceptors.Validation(stubValidator{}))

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	require.NoError(t, err)
	assert.Equal(t, 1, svc.Calls())
}

func TestValidation_InvalidRequestIsRejectedBeforeHandler(t *testing.T) {
	t.Parallel()

	validator := stubValidator{err: &protovalidate.ValidationError{
		Violations: []*protovalidate.Violation{fieldViolation("service", "value is required")},
	}}
	client, svc := newHealthClient(t, okCheck, interceptors.Error(), interceptors.Validation(validator))

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, 0, svc.Calls(), "handler must not be called for an invalid request")

	var violations []*errdetails.BadRequest_FieldViolation
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			violations = br.GetFieldViolations()
		}
	}
	require.Len(t, violations, 1)
	assert.Equal(t, "service", violations[0].GetField())
	assert.Equal(t, "value is required", violations[0].GetDescription())
}

func TestValidation_ValidatorFailureIsInternal(t *testing.T) {
	t.Parallel()

	validator := stubValidator{err: errors.New("cel runtime failure")}
	client, svc := newHealthClient(t, okCheck, interceptors.Error(), interceptors.Validation(validator))

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Equal(t, apperr.MessageInternal, st.Message())
	assert.Equal(t, 0, svc.Calls())
}

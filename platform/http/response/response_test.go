package response_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/http/response"
)

const contentType = "application/json; charset=utf-8"

func newRequest(ctx context.Context) *http.Request {
	return httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		write      func(http.ResponseWriter, *http.Request, any)
		wantStatus int
	}{
		{"ok", response.OK, http.StatusOK},
		{"created", response.Created, http.StatusCreated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := newRequest(appctx.WithRequestID(context.Background(), "req-1"))

			tc.write(rec, req, map[string]string{"id": "42"})

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, contentType, rec.Header().Get("Content-Type"))
			body := decode(t, rec)
			assert.Equal(t, map[string]any{"id": "42"}, body["data"])
			assert.Nil(t, body["error"])
			assert.Equal(t, map[string]any{"request_id": "req-1"}, body["meta"])
		})
	}
}

func TestOK_withoutRequestID(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()

	response.OK(rec, newRequest(context.Background()), "x")

	assert.Equal(t, map[string]any{}, decode(t, rec)["meta"])
}

func TestFail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantReason  string
	}{
		{"not found", apperr.NotFound(), http.StatusNotFound, "NOT_FOUND", apperr.MessageNotFound, ""},
		{
			"custom message and reason",
			apperr.AlreadyExists().WithMessage("order exists").WithReason("DUP"),
			http.StatusConflict, "ALREADY_EXISTS", "order exists", "DUP",
		},
		{
			"wrapped apperr",
			errors.Join(errors.New("ctx"), apperr.PermissionDenied()),
			http.StatusForbidden, "PERMISSION_DENIED", apperr.MessagePermissionDenied, "",
		},
		{
			"plain error hides cause",
			errors.New("db password leaked"),
			http.StatusInternalServerError, "INTERNAL", apperr.MessageInternal, "",
		},
		{"context canceled", context.Canceled, 499, "CANCELED", apperr.MessageCanceled, ""},
		{
			"deadline exceeded",
			context.DeadlineExceeded,
			http.StatusGatewayTimeout, "DEADLINE_EXCEEDED", apperr.MessageDeadlineExceeded, "",
		},
		{
			"invalid argument",
			apperr.InvalidArgument(),
			http.StatusBadRequest, "INVALID_ARGUMENT", apperr.MessageInvalidArgument, "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			ctx := appctx.WithErrBox(appctx.WithRequestID(context.Background(), "req-1"))

			response.Fail(rec, newRequest(ctx), tc.err)

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, contentType, rec.Header().Get("Content-Type"))
			body := decode(t, rec)
			assert.Nil(t, body["data"])
			assert.Equal(t, map[string]any{"request_id": "req-1"}, body["meta"])

			errBody, ok := body["error"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.wantCode, errBody["code"])
			assert.Equal(t, tc.wantMessage, errBody["message"])
			if tc.wantReason != "" {
				assert.Equal(t, tc.wantReason, errBody["reason"])
			} else {
				assert.NotContains(t, errBody, "reason")
			}

			stored := appctx.GetErr(ctx)
			require.Error(t, stored)
			assert.Equal(t, tc.wantCode, apperr.From(stored).Code.String())
		})
	}
}

func TestFail_violationsInBody(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	err := apperr.InvalidArgument().WithViolations(apperr.Violation{Field: "name", Message: "required"})

	response.Fail(rec, newRequest(context.Background()), err)

	errBody, ok := decode(t, rec)["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{map[string]any{"field": "name", "message": "required"}}, errBody["violations"])
}

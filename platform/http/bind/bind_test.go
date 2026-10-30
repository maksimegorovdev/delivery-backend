package bind_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/http/bind"
)

type payload struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func (p payload) Validate() error {
	return validation.ValidateStruct(&p,
		validation.Field(&p.Name, validation.Required),
		validation.Field(&p.Age, validation.Min(0)),
	)
}

func newRequest(tb testing.TB, body string) *http.Request {
	tb.Helper()

	return httptest.NewRequestWithContext(tb.Context(), http.MethodPost, "/", strings.NewReader(body))
}

func TestJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		body           string
		want           payload
		wantReason     string
		wantViolations []apperr.Violation
	}{
		{
			name: "valid body",
			body: `{"name":"alice","age":30}`,
			want: payload{Name: "alice", Age: 30},
		},
		{
			name: "unknown fields are ignored",
			body: `{"name":"alice","extra":true}`,
			want: payload{Name: "alice"},
		},
		{
			name:       "malformed json",
			body:       `{"name":`,
			wantReason: apperr.ReasonInvalidRequestBody,
		},
		{
			name:       "empty body",
			body:       ``,
			wantReason: apperr.ReasonInvalidRequestBody,
		},
		{
			name:       "wrong field type",
			body:       `{"name":1}`,
			want:       payload{},
			wantReason: apperr.ReasonInvalidRequestBody,
		},
		{
			name:       "validation error",
			body:       `{"name":"","age":-1}`,
			want:       payload{Age: -1},
			wantReason: apperr.ReasonValidationFailed,
			wantViolations: []apperr.Violation{
				{Field: "age", Message: "must be no less than 0"},
				{Field: "name", Message: "cannot be blank"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := bind.JSON[payload](newRequest(t, tt.body))

			assert.Equal(t, tt.want, got)
			if tt.wantReason == "" {
				require.NoError(t, err)
				return
			}

			appErr, ok := apperr.As(err)
			require.True(t, ok, "want apperr.Error, got %v", err)
			assert.Equal(t, apperr.CodeInvalidArgument, appErr.Code)
			assert.Equal(t, tt.wantReason, appErr.Reason)
			assert.Equal(t, tt.wantViolations, appErr.Violations)
		})
	}
}

func FuzzJSON(f *testing.F) {
	f.Add(`{"name":"alice","age":30}`)
	f.Add(``)
	f.Add(`null`)
	f.Add(`{"name":"alice"`)
	f.Add(`{"name":"alice","unknown":[1,2,{"a":null}]}`)

	f.Fuzz(func(t *testing.T, body string) {
		_, err := bind.JSON[payload](newRequest(t, body))
		if err == nil {
			return
		}

		appErr, ok := apperr.As(err)
		require.True(t, ok, "want apperr.Error, got %v", err)
		require.Equal(t, apperr.CodeInvalidArgument, appErr.Code)
	})
}

package ozzoerr_test

import (
	"errors"
	"fmt"
	"testing"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr/ozzoerr"
)

type address struct {
	City string `json:"city"`
}

func (a address) Validate() error {
	return validation.ValidateStruct(&a,
		validation.Field(&a.City, validation.Required),
	)
}

type request struct {
	Name    string  `json:"name"`
	Email   string  `json:"email"`
	Address address `json:"address"`
}

func (r request) Validate() error {
	return validation.ValidateStruct(&r,
		validation.Field(&r.Name, validation.Required),
		validation.Field(&r.Email, validation.Required, validation.Length(5, 0)),
		validation.Field(&r.Address),
	)
}

func TestFlatten(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
		err    error
		want   []apperr.Violation
	}{
		{
			name: "single error without prefix",
			err:  errors.New("bad value"),
			want: []apperr.Violation{{Field: "", Message: "bad value"}},
		},
		{
			name:   "single error uses prefix as field",
			prefix: "items.0",
			err:    errors.New("bad value"),
			want:   []apperr.Violation{{Field: "items.0", Message: "bad value"}},
		},
		{
			name: "errors are sorted by field",
			err: validation.Errors{
				"zip":  errors.New("z"),
				"city": errors.New("c"),
				"name": errors.New("n"),
			},
			want: []apperr.Violation{
				{Field: "city", Message: "c"},
				{Field: "name", Message: "n"},
				{Field: "zip", Message: "z"},
			},
		},
		{
			name:   "prefix is prepended to every field",
			prefix: "body",
			err:    validation.Errors{"name": errors.New("n"), "email": errors.New("e")},
			want: []apperr.Violation{
				{Field: "body.email", Message: "e"},
				{Field: "body.name", Message: "n"},
			},
		},
		{
			name: "nested errors give dotted path",
			err: validation.Errors{
				"address": validation.Errors{
					"city": errors.New("c"),
					"geo":  validation.Errors{"lat": errors.New("l")},
				},
				"name": errors.New("n"),
			},
			want: []apperr.Violation{
				{Field: "address.city", Message: "c"},
				{Field: "address.geo.lat", Message: "l"},
				{Field: "name", Message: "n"},
			},
		},
		{
			name: "wrapped validation errors are unwrapped",
			err:  fmt.Errorf("validate: %w", validation.Errors{"name": errors.New("n")}),
			want: []apperr.Violation{{Field: "name", Message: "n"}},
		},
		{
			name: "empty errors give no violations",
			err:  validation.Errors{},
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, ozzoerr.Flatten(tc.prefix, tc.err))
		})
	}
}

func TestMap(t *testing.T) {
	t.Parallel()

	t.Run("nil stays nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, ozzoerr.Map(nil))
	})

	t.Run("validation errors give invalid argument with field list", func(t *testing.T) {
		t.Parallel()

		src := validation.Errors{"name": errors.New("cannot be blank"), "email": errors.New("too short")}

		got, ok := apperr.As(ozzoerr.Map(src))

		require.True(t, ok)
		require.Equal(t, apperr.CodeInvalidArgument, got.Code)
		require.Equal(t, apperr.ReasonValidationFailed, got.Reason)
		require.Equal(t, apperr.ReasonMessageValidationFailed, got.Public())
		require.Equal(t, []apperr.Violation{
			{Field: "email", Message: "too short"},
			{Field: "name", Message: "cannot be blank"},
		}, got.Violations)
	})

	t.Run("single error gives one violation without field", func(t *testing.T) {
		t.Parallel()

		got, ok := apperr.As(ozzoerr.Map(errors.New("bad body")))

		require.True(t, ok)
		require.Equal(t, apperr.CodeInvalidArgument, got.Code)
		require.Equal(t, []apperr.Violation{{Field: "", Message: "bad body"}}, got.Violations)
	})

	t.Run("result matches apperr invalid argument", func(t *testing.T) {
		t.Parallel()

		require.ErrorIs(t, ozzoerr.Map(errors.New("x")), apperr.ErrInvalidArgument)
	})

	t.Run("real ValidateStruct output uses json field names", func(t *testing.T) {
		t.Parallel()

		got, ok := apperr.As(ozzoerr.Map(request{Email: "a@b", Address: address{}}.Validate()))

		require.True(t, ok)
		require.Equal(t, []apperr.Violation{
			{Field: "address.city", Message: "cannot be blank"},
			{Field: "email", Message: "the length must be no less than 5"},
			{Field: "name", Message: "cannot be blank"},
		}, got.Violations)
	})

	t.Run("valid struct gives nil", func(t *testing.T) {
		t.Parallel()

		valid := request{Name: "n", Email: "a@b.co", Address: address{City: "c"}}

		require.NoError(t, ozzoerr.Map(valid.Validate()))
	})
}

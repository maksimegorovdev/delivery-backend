package response

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

type Meta struct {
	RequestID string `json:"request_id,omitempty"`
}

type ErrorBody struct {
	Code       string             `json:"code"`
	Reason     string             `json:"reason,omitempty"`
	Message    string             `json:"message"`
	Violations []apperr.Violation `json:"violations,omitempty"`
}

type Envelope struct {
	Meta  Meta       `json:"meta"`
	Data  any        `json:"data"`
	Error *ErrorBody `json:"error"`
}

func meta(ctx context.Context) Meta {
	return Meta{
		RequestID: appctx.GetRequestID(ctx),
	}
}

func write(w http.ResponseWriter, status int, envelope Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope)
}

func OK(w http.ResponseWriter, r *http.Request, data any) {
	write(w, http.StatusOK, Envelope{
		Meta: meta(r.Context()),
		Data: data,
	})
}

func Created(w http.ResponseWriter, r *http.Request, data any) {
	write(w, http.StatusCreated, Envelope{
		Meta: meta(r.Context()),
		Data: data,
	})
}

func Fail(w http.ResponseWriter, r *http.Request, err error) {
	e := apperr.From(err)
	appctx.SetErr(r.Context(), e)

	write(w, e.Code.HTTP(), Envelope{
		Meta: meta(r.Context()),
		Error: &ErrorBody{
			Code:       e.Code.String(),
			Reason:     e.Reason,
			Message:    e.Public(),
			Violations: e.Violations,
		},
	})
}

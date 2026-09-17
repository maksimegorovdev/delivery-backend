package bind

import (
	"encoding/json"
	"net/http"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr/ozzoerr"
)

type Validatable interface {
	Validate() error
}

func JSON[T Validatable](r *http.Request) (T, error) {
	var body T
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return body, apperr.InvalidRequestBody().Wrap(err)
	}
	if err := ozzoerr.Map(body.Validate()); err != nil {
		return body, err
	}
	return body, nil
}

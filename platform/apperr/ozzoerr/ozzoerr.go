package ozzoerr

import (
	"sort"

	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

func Flatten(prefix string, err error) []apperr.Violation {
	verrs, ok := err.(validation.Errors)
	if !ok {
		return []apperr.Violation{
			{
				Field:   prefix,
				Message: err.Error(),
			},
		}
	}

	keys := make([]string, 0, len(verrs))
	for k := range verrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []apperr.Violation
	for _, k := range keys {
		field := k
		if prefix != "" {
			field = prefix + "." + k
		}
		out = append(out, Flatten(field, verrs[k])...)
	}
	return out
}

func Map(err error) error {
	if err == nil {
		return nil
	}
	return apperr.ValidationFailed(Flatten("", err)...)
}

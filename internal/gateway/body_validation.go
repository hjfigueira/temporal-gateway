package gateway

import (
	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/validate"
)

// validateBody enforces the operation's requestBody.required flag and, when
// an application/json schema is declared, validates body against it. The
// returned Result.Status.IsError() is false when there's nothing to report.
func validateBody(rb *spec.RequestBody, body any) validate.Result {
	if rb == nil {
		return validate.Result{Envelope: response.Envelope{Status: response.StatusValid}}
	}
	if body == nil {
		if rb.Required {
			return validate.Result{Envelope: response.Envelope{Status: response.StatusInvalidRequest, Message: "request body is required"}}
		}
		return validate.Result{Envelope: response.Envelope{Status: response.StatusValid}}
	}
	media, ok := rb.Content["application/json"]
	if !ok || len(media.Schema) == 0 {
		return validate.Result{Envelope: response.Envelope{Status: response.StatusValid}}
	}
	return validate.Schema(media.Schema, body)
}

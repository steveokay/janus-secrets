// Package api is Janus's HTTP surface: the chi router, the /v1/sys/* seal
// lifecycle endpoints, the RequireUnsealed middleware, and the project-wide
// JSON error envelope. Handlers are thin translation layers; all seal logic
// lives in internal/crypto.
package api

import (
	"encoding/json"
	"net/http"
)

// Error codes used in the {"error":{"code","message"}} envelope. These are the
// project-wide vocabulary; later milestones add to it.
const (
	CodeSealed             = "sealed"
	CodeNotInitialized     = "not_initialized"
	CodeAlreadyInitialized = "already_initialized"
	CodeInvalidShare       = "invalid_share"
	CodeDuplicateShare     = "duplicate_share"
	CodeNotEnoughShares    = "not_enough_shares"
	CodeKeyCheckFailed     = "key_check_failed"
	CodeValidation         = "validation"
	CodeInternal           = "internal"
	CodeUnauthenticated    = "unauthenticated"
	CodeRateLimited        = "rate_limited"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes the project error envelope. message must never contain
// internals, key material, or secret values.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

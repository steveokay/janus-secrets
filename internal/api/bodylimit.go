package api

import (
	"net/http"
	"strings"
)

// bodyLimit caps the request body at maxBytes via http.MaxBytesReader (a later
// read past the cap fails with a 413). maxBytes<=0 disables the cap. POST
// /v1/sys/restore is exempt: it streams a full instance backup (unbounded by
// design, with its own per-record 64MB bound).
func bodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maxBytes > 0 && !(r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/sys/restore")) {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

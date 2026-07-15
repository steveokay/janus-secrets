package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

var (
	errBadLimit  = errors.New("limit must be an integer 1-200")
	errBadCursor = errors.New("cursor is malformed")
)

// pageParams is the parsed pagination request. limit==0 means unbounded (no
// limit param supplied — the backward-compatible path); after==nil is the first
// page.
type pageParams struct {
	limit int
	after *store.Cursor
}

// cursorPayload is the JSON encoded inside the opaque base64url cursor token.
type cursorPayload struct {
	T time.Time `json:"t"`
	I string    `json:"i"`
}

// parsePageParams reads ?limit and ?cursor. Missing limit → 0 (unbounded).
func parsePageParams(r *http.Request) (pageParams, error) {
	var pp pageParams
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return pp, errBadLimit
		}
		pp.limit = n
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		c, err := decodeCursor(v)
		if err != nil {
			return pp, errBadCursor
		}
		pp.after = c
	}
	return pp, nil
}

// encodeCursor produces the opaque continuation token for a row's keyset
// position.
func encodeCursor(createdAt time.Time, id string) string {
	b, _ := json.Marshal(cursorPayload{T: createdAt, I: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (*store.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.I == "" {
		return nil, errBadCursor
	}
	return &store.Cursor{CreatedAt: p.T, ID: p.I}, nil
}

// nextCursor returns the encoded continuation token when the page was full
// (len == limit and limit > 0), else nil — computed from the last RAW scanned
// row's keyset position (createdAt,id), independent of any post-filtering.
func nextCursor(limit, rawLen int, lastCreatedAt time.Time, lastID string) *string {
	if limit <= 0 || rawLen < limit {
		return nil
	}
	tok := encodeCursor(lastCreatedAt, lastID)
	return &tok
}

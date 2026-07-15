package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParsePageParams(t *testing.T) {
	// no params → unbounded, no cursor
	pp, err := parsePageParams(httptest.NewRequest("GET", "/v1/projects", nil))
	if err != nil || pp.limit != 0 || pp.after != nil {
		t.Fatalf("no params: %+v err=%v", pp, err)
	}
	// valid limit
	pp, err = parsePageParams(httptest.NewRequest("GET", "/v1/projects?limit=25", nil))
	if err != nil || pp.limit != 25 {
		t.Fatalf("limit=25: %+v err=%v", pp, err)
	}
	// out-of-range limit → error
	if _, err := parsePageParams(httptest.NewRequest("GET", "/v1/projects?limit=0", nil)); err == nil {
		t.Fatal("limit=0 should error")
	}
	if _, err := parsePageParams(httptest.NewRequest("GET", "/v1/projects?limit=201", nil)); err == nil {
		t.Fatal("limit=201 should error")
	}
	// round-trip cursor
	ts := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	tok := encodeCursor(ts, "id-1")
	pp, err = parsePageParams(httptest.NewRequest("GET", "/v1/projects?limit=10&cursor="+tok, nil))
	if err != nil || pp.after == nil || !pp.after.CreatedAt.Equal(ts) || pp.after.ID != "id-1" {
		t.Fatalf("cursor round-trip: %+v err=%v", pp, err)
	}
	// malformed cursor → error
	if _, err := parsePageParams(httptest.NewRequest("GET", "/v1/projects?cursor=!!notbase64!!", nil)); err == nil {
		t.Fatal("bad cursor should error")
	}
}

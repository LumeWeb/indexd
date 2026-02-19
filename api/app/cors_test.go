package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.sia.tech/jape"
)

func TestCORSOptions(t *testing.T) {
	// since this is for OPTIONs testing, the handlers themselves should not be called
	panicHandler := func(jc jape.Context) {
		panic("handler should not have been called")
	}
	mux := corsMux(
		map[string]jape.Handler{
			"GET /auth/connect":               panicHandler,
			"GET /auth/connect/:id/status":    panicHandler,
			"POST /auth/connect/:id/register": panicHandler,
			"GET /auth/check":                 panicHandler,
		},
		map[string]jape.Handler{
			// both disabled routes have the same path but different methods,
			// should be allowed since they would not conflict with each other
			// or the OPTIONS route.
			"GET /auth/connect/:id":  panicHandler,
			"POST /auth/connect/:id": panicHandler,
		},
	)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	assertOptionsResponse := func(t *testing.T, path string, expected int) {
		t.Helper()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		} else if resp.StatusCode != expected {
			t.Fatalf("expected status code %d, got %d", expected, resp.StatusCode)
		}
	}

	enabledRoutes := []string{
		"/auth/connect",
		"/auth/connect/asdasd/status",
		"/auth/connect/asdasd/register",
		"/auth/check",
	}

	disabledRoutes := []string{
		"/auth/connect/asdasd", // both disabled routes have the same path but different methods.
	}

	for _, path := range enabledRoutes {
		assertOptionsResponse(t, path, http.StatusNoContent)
	}

	for _, path := range disabledRoutes {
		assertOptionsResponse(t, path, http.StatusMethodNotAllowed)
	}
}

func TestCORSOptionsDuplicates(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic due to duplicate route, but did not panic")
		}
	}()

	// since this is for OPTIONs testing, the handlers themselves should not be called
	panicHandler := func(jc jape.Context) {
		panic("handler should not have been called")
	}

	corsMux(map[string]jape.Handler{
		"GET /auth/connect": panicHandler,
	}, map[string]jape.Handler{
		"POST /auth/:id": panicHandler,
	})
}

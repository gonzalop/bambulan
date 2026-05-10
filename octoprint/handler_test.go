package octoprint

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gonzalop/bambulan"
)

func TestHandler_Auth(t *testing.T) {
	apiKey := "test-key"
	h := NewHandler(func() (*bambulan.Client, *bambulan.PrinterStatus, bool) {
		return nil, nil, false
	}, apiKey)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// 1. Request without API key should fail
	req := httptest.NewRequest("GET", "/api/version", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden, got %d", rr.Code)
	}

	// 2. Request with correct API key header should pass (though session might still fail later)
	req = httptest.NewRequest("GET", "/api/version", nil)
	req.Header.Set("X-Api-Key", apiKey)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code == http.StatusForbidden {
		t.Error("Expected request to pass auth, but got 403")
	}

	// 3. Request with correct API key in query param should pass
	req = httptest.NewRequest("GET", "/api/version?apikey="+apiKey, nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code == http.StatusForbidden {
		t.Error("Expected query param auth to pass, but got 403")
	}
}

func TestHandler_Version(t *testing.T) {
	apiKey := "test-key"
	h := NewHandler(nil, apiKey)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/version", nil)
	req.Header.Set("X-Api-Key", apiKey)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected application/json, got %q", contentType)
	}
}

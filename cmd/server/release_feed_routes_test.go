package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tabloy/keygate/internal/handler"
)

func TestRegisterReleaseFeedRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	registerReleaseFeedRoutes(v1, handler.NewReleasePublicHandler(handler.ReleasePublicConfig{}), 240)

	wantRoutes := map[string]bool{
		"GET /api/v1/releases/:product_slug/feed.xml":     false,
		"GET /api/v1/releases/:product_slug/feed.json":    false,
		"GET /api/v1/releases/:product_slug/upgrade.json": false,
		"GET /api/v1/releases/feed.xml":                   false,
		"GET /api/v1/releases/feed.json":                  false,
		"GET /api/v1/releases/upgrade.json":               false,
		"GET /api/v1/releases/feed":                       false,
	}
	for _, route := range r.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := wantRoutes[key]; ok {
			wantRoutes[key] = true
		}
	}
	for route, found := range wantRoutes {
		if !found {
			t.Errorf("route %s was not registered", route)
		}
	}

	for _, path := range []string{
		"/api/v1/releases/feed.xml",
		"/api/v1/releases/feed.json",
		"/api/v1/releases/upgrade.json",
		"/api/v1/releases/feed",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			r.ServeHTTP(w, req)
			if w.Code != http.StatusGone {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusGone)
			}
			if !strings.Contains(w.Body.String(), "FEED_PATH_REMOVED") {
				t.Fatalf("body = %q, want FEED_PATH_REMOVED", w.Body.String())
			}
		})
	}
}

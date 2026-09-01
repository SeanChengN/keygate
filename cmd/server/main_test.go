package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tabloy/keygate/internal/config"
	"github.com/tabloy/keygate/internal/storage"
)

func TestInitializeReleaseStorage(t *testing.T) {
	t.Run("disabled when storage is not configured", func(t *testing.T) {
		got, err := initializeReleaseStorage(&config.Config{})
		if err != nil {
			t.Fatalf("initializeReleaseStorage() error = %v", err)
		}
		if _, ok := got.(storage.Disabled); !ok {
			t.Fatalf("initializeReleaseStorage() type = %T, want storage.Disabled", got)
		}
	})

	t.Run("configured invalid endpoint fails closed", func(t *testing.T) {
		_, err := initializeReleaseStorage(&config.Config{
			StorageEndpoint:       "http://minio.internal:9000",
			StorageRegion:         "us-east-1",
			StorageBucket:         "artifacts",
			StorageAccessKey:      "test-access-key",
			StorageSecretKey:      "test-secret-key",
			StorageForcePathStyle: true,
		})
		if err == nil {
			t.Fatal("initializeReleaseStorage() error = nil, want configured storage failure")
		}
	})
}

func TestRequireBearerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const token = "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name   string
		header string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong", status: http.StatusUnauthorized},
		{name: "wrong scheme", header: token, status: http.StatusUnauthorized},
		{name: "valid", header: "Bearer " + token, status: http.StatusNoContent},
		{name: "case insensitive scheme", header: "bearer " + token, status: http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/protected", requireBearerToken(token), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d", w.Code, tt.status)
			}
		})
	}
}

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestSessionAuthRejectsRevokedGeneration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "0123456789abcdef0123456789abcdef"
	token, err := IssueJWT(secret, "user-1", "admin@example.com", "Admin", true, 3, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name       string
		generation int64
		want       int
	}{
		{name: "current generation", generation: 3, want: http.StatusNoContent},
		{name: "revoked generation", generation: 4, want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			checker := func(context.Context, string) (bool, int64, error) {
				return true, tc.generation, nil
			}
			r.GET("/protected", SessionAuth(secret, checker), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)
			if resp.Code != tc.want {
				t.Fatalf("status = %d, want %d", resp.Code, tc.want)
			}
		})
	}
}

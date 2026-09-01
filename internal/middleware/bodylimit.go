package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxRequestBody caps every request body before a handler attempts to decode
// it. Routes with a lower domain-specific cap may wrap the body again.
func MaxRequestBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxBytes > 0 && c.Request.ContentLength > maxBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"success": false,
				"error":   gin.H{"code": "REQUEST_TOO_LARGE", "message": "request body exceeds the configured limit"},
			})
			return
		}
		if c.Request.Body != nil && maxBytes > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

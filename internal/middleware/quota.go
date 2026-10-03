package middleware

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"backend/internal/services"

	"github.com/gin-gonic/gin"
)

// QuotaMiddleware corta la petición antes de llegar al RAG cuando el usuario
// ya consumió su cuota diaria de tokens. Debe ir después de AuthMiddleware.
func QuotaMiddleware(quotaService *services.QuotaService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid or expired token",
				"code":  "UNAUTHORIZED",
			})
			c.Abort()
			return
		}

		status, err := quotaService.Status(c.Request.Context(), userID)
		if err != nil {
			if errors.Is(err, services.ErrUserNotFound) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "Invalid or expired token",
					"code":  "UNAUTHORIZED",
				})
			} else {
				// Sin poder verificar la cuota no se deja pasar la petición.
				log.Printf("quota: %v", err)
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": "No se pudo verificar tu cuota de tokens, intenta más tarde",
					"code":  "QUOTA_CHECK_FAILED",
				})
			}
			c.Abort()
			return
		}

		if status.Exhausted() {
			c.Header("Retry-After", strconv.Itoa(services.SecondsUntilReset(time.Now())))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "Se te acabaron los tokens de hoy. Tu cuota se reinicia a medianoche.",
				"code":        "TOKENS_EXHAUSTED",
				"used_tokens": status.Used,
				"max_tokens":  status.Max,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

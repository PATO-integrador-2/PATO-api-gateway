package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"backend/internal/repository"

	"github.com/gin-gonic/gin"
)

const (
	defaultConversationLimit = 50
	maxConversationLimit     = 200
)

type ConversationHandler struct {
	repo *repository.ConversationRepository
}

func NewConversationHandler(repo *repository.ConversationRepository) *ConversationHandler {
	return &ConversationHandler{repo: repo}
}

// List — GET /api/conversations
func (h *ConversationHandler) List(c *gin.Context) {
	userID, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized",
			"code":  "UNAUTHORIZED",
		})
		return
	}

	limit, offset := parsePagination(c)

	convs, err := h.repo.ListByUser(
		c.Request.Context(),
		userID.(string),
		limit,
		offset,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "no se pudieron obtener las conversaciones",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":       userID, // ← añade esto
		"conversations": convs,
		"limit":         limit,
		"offset":        offset,
		"has_more":      len(convs) == limit,
	})
}

// GetMessages — GET /api/conversations/:id/messages
func (h *ConversationHandler) GetMessages(c *gin.Context) {
	userID, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized",
			"code":  "UNAUTHORIZED",
		})
		return
	}

	conversationID := c.Param("id")
	if conversationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "id de conversación requerido",
		})
		return
	}

	limit, offset := parsePagination(c)

	// Pedimos limit+1 para saber si hay más sin un COUNT extra.
	msgs, err := h.repo.GetMessages(
		c.Request.Context(),
		conversationID,
		userID.(string),
		limit+1,
		offset,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "no se pudieron obtener los mensajes",
		})
		return
	}

	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}

	// Si no hay mensajes en el primer offset, la conversación no existe
	// o no pertenece al usuario. Devolvemos 404 sin distinguir cuál.
	if len(msgs) == 0 && offset == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "conversación no encontrada",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          conversationID,
		"messages":    msgs,
		"limit":       limit,
		"offset":      offset,
		"has_more":    hasMore,
		"next_offset": offset + len(msgs),
	})
}

// Delete — DELETE /api/conversations/:id
func (h *ConversationHandler) Delete(c *gin.Context) {
	userID, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized",
			"code":  "UNAUTHORIZED",
		})
		return
	}

	conversationID := c.Param("id")

	err := h.repo.SoftDelete(
		c.Request.Context(),
		conversationID,
		userID.(string),
	)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "conversación no encontrada",
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "no se pudo eliminar la conversación",
		})
		return
	}

	c.Status(http.StatusNoContent)
}

func parsePagination(c *gin.Context) (int, int) {
	limit := defaultConversationLimit
	offset := 0

	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxConversationLimit {
				n = maxConversationLimit
			}
			limit = n
		}
	}

	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	return limit, offset
}

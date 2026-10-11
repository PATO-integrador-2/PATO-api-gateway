package handlers

import (
	"errors"
	"net/http"
	"strings"

	"backend/internal/services"

	"github.com/gin-gonic/gin"
)

type AIHandler struct {
	aiService *services.AIService
}

func NewAIHandler(aiService *services.AIService) *AIHandler {
	return &AIHandler{
		aiService: aiService,
	}
}

// Predict reenvía la solicitud de predicción a la API de IA (POST /predictions/:type)
// y traduce sus fallos a respuestas HTTP consistentes para el cliente.
func (h *AIHandler) Predict(c *gin.Context) {
	predictionType := c.Param("type")

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Datos inválidos",
		})
		return
	}

	data, err := h.aiService.Predict(predictionType, payload)
	if err != nil {
		var upstreamErr *services.PredictionUpstreamError

		switch {
		case errors.Is(err, services.ErrPredictionTypeNotAllowed):
			c.JSON(http.StatusBadRequest, gin.H{
				"error": err.Error(),
			})

		case errors.As(err, &upstreamErr):
			status := upstreamErr.StatusCode
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			c.JSON(status, gin.H{
				"error":  "El servicio de predicción no pudo generar el resultado",
				"detail": upstreamErr.Body,
			})

		default:
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "No se pudo contactar al servicio de predicción",
			})
		}
		return
	}

	c.JSON(http.StatusOK, data)
}

// ExportPDF reenvía a la API de IA un conjunto de datos (título + registros,
// normalmente el `data` obtenido de /predictions/:type) y devuelve el PDF
// generado como archivo descargable (POST /export/pdf).
func (h *AIHandler) ExportPDF(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Datos inválidos",
		})
		return
	}

	pdfBytes, err := h.aiService.ExportPDF(payload)
	if err != nil {
		var upstreamErr *services.PredictionUpstreamError

		switch {
		case errors.As(err, &upstreamErr):
			status := upstreamErr.StatusCode
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			c.JSON(status, gin.H{
				"error":  "El servicio de predicción no pudo generar el PDF",
				"detail": upstreamErr.Body,
			})

		default:
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "No se pudo contactar al servicio de predicción",
			})
		}
		return
	}

	c.Header("Content-Disposition", `attachment; filename="reporte.pdf"`)
	c.Data(http.StatusOK, "application/pdf", pdfBytes)
}

// ExportPredictionPDF calcula la predicción del tipo indicado y devuelve
// directamente el PDF, en una sola llamada (POST /exports/pdf/:type).
// El body es el mismo que se usaría en /predictions/:type.
func (h *AIHandler) ExportPredictionPDF(c *gin.Context) {
	predictionType := c.Param("type")

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Datos inválidos",
		})
		return
	}

	pdfBytes, err := h.aiService.ExportPredictionPDF(predictionType, payload)
	if err != nil {
		var upstreamErr *services.PredictionUpstreamError

		switch {
		case errors.Is(err, services.ErrPredictionTypeNotAllowed):
			c.JSON(http.StatusBadRequest, gin.H{
				"error": err.Error(),
			})

		case errors.As(err, &upstreamErr):
			status := upstreamErr.StatusCode
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			c.JSON(status, gin.H{
				"error":  "El servicio de predicción no pudo generar el PDF",
				"detail": upstreamErr.Body,
			})

		default:
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "No se pudo contactar al servicio de predicción",
			})
		}
		return
	}

	c.Header("Content-Disposition", `attachment; filename="reporte.pdf"`)
	c.Data(http.StatusOK, "application/pdf", pdfBytes)
}

// Ask reenvía una pregunta al endpoint /ask del servicio de IA
// y devuelve la respuesta del orquestador (data / simulation / rag / recommendation).
func (h *AIHandler) Ask(c *gin.Context) {
	var req services.AskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Datos inválidos: se requiere 'question'",
		})
		return
	}

	if strings.TrimSpace(req.Question) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "La pregunta no puede estar vacía",
		})
		return
	}

	// El AuthMiddleware dejó el userID en el contexto (c.Set("userID", ...)).
	userID := ""
	if v, ok := c.Get("userID"); ok {
		if s, ok := v.(string); ok {
			userID = s
		}
	}

	data, err := h.aiService.Ask(c.Request.Context(), userID, req)
	if err != nil {
		var upstreamErr *services.PredictionUpstreamError

		if errors.As(err, &upstreamErr) {
			status := upstreamErr.StatusCode
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			c.JSON(status, gin.H{
				"error": "El servicio de IA no pudo procesar la pregunta",
			})
			return
		}

		c.JSON(http.StatusBadGateway, gin.H{
			"error": "No se pudo contactar al servicio de IA",
		})
		return
	}

	c.JSON(http.StatusOK, data)
}

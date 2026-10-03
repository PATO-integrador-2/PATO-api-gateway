package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"backend/internal/services"

	"github.com/gin-gonic/gin"
)

// RAGHandler reenvía las preguntas al RAG (IA generativa con Qwen) y contabiliza
// los tokens consumidos contra la cuota diaria del usuario.
type RAGHandler struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	quota      *services.QuotaService
	estimator  services.TokenEstimator
}

func NewRAGHandler(
	baseURL, apiKey string,
	timeout time.Duration,
	quota *services.QuotaService,
	estimator services.TokenEstimator,
) *RAGHandler {
	return &RAGHandler{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
		quota:      quota,
		estimator:  estimator,
	}
}

// Ask reenvía la pregunta a POST /api/rag/ask (POST /api/users/chat/ask).
//
// El user_id se toma siempre del token validado por el gateway: se ignora el
// que envíe el cliente para que nadie consuma la cuota de otro usuario.
func (h *RAGHandler) Ask(c *gin.Context) {
	userID := c.GetString("userID")

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos"})
		return
	}

	question, _ := payload["question"].(string)
	if strings.TrimSpace(question) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "La pregunta es obligatoria"})
		return
	}

	payload["user_id"] = userID

	body, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos"})
		return
	}

	req, err := http.NewRequestWithContext(
		c.Request.Context(),
		http.MethodPost,
		h.baseURL+"/api/rag/ask",
		bytes.NewReader(body),
	)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "No se pudo contactar al servicio de IA generativa"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.apiKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		log.Printf("rag: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "No se pudo contactar al servicio de IA generativa"})
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "No se pudo leer la respuesta del servicio de IA generativa"})
		return
	}

	// Errores del RAG: se devuelven tal cual, sin cobrar tokens.
	if resp.StatusCode != http.StatusOK {
		c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Respuesta inválida del servicio de IA generativa"})
		return
	}

	answer, _ := result["answer"].(string)
	chunksUsed, _ := result["chunks_used"].(float64)
	conversationID, _ := payload["conversation_id"].(string)

	tokens := h.estimator.Ask(question, answer, int(chunksUsed), conversationID != "")

	// La respuesta ya se generó: se cobra aunque falle el registro (se loguea).
	// Se usa un contexto propio para que se registre aunque el cliente cierre la conexión.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	usage := gin.H{"tokens_estimated": tokens}

	if used, err := h.quota.AddUsage(ctx, userID, tokens); err != nil {
		log.Printf("quota: no se pudo registrar %d tokens del usuario %s: %v", tokens, userID, err)
	} else {
		usage["used_tokens"] = used
		if status, err := h.quota.Status(ctx, userID); err == nil && !status.Unlimited {
			usage["max_tokens"] = status.Max
			usage["remaining_tokens"] = max(status.Max-status.Used, 0)
		}
	}

	result["usage"] = usage
	c.JSON(http.StatusOK, result)
}

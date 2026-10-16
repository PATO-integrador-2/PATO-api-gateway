package services

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ============================================================
// ChatbotService — IA generativa (orquestador /ask)
// ============================================================
//
// Este servicio es DISTINTO de AIService (predicciones + PDF).
// Apunta a un backend Python distinto (por defecto :8010) que expone
// el orquestador RAG/SQL/simulación/recomendación.

type ChatbotService struct {
	baseURL    string
	httpClient *http.Client
	usersDB    *sql.DB // user_management_db; nil = no se pueden crear conversaciones
}

func NewChatbotService(baseURL string, usersDB *sql.DB) *ChatbotService {
	return &ChatbotService{
		baseURL: baseURL,
		usersDB: usersDB,
		httpClient: &http.Client{
			// El orquestador en CPU puede tardar 30–60s por pregunta.
			Timeout: 120 * time.Second,
		},
	}
}

// CreateConversation crea una fila en ai_conversation (user_management_db)
// y devuelve su id. El RAG exige un conversation_id existente.
func (s *ChatbotService) CreateConversation(ctx context.Context, userID, firstMessage string) (string, error) {
	if s.usersDB == nil {
		return "", fmt.Errorf("USERS_DATABASE_URL no configurada")
	}
	name := []rune(strings.TrimSpace(firstMessage))
	if len(name) > 60 {
		name = name[:60]
	}
	var id string
	err := s.usersDB.QueryRowContext(ctx,
		`INSERT INTO ai_conversation (user_id, name) VALUES ($1, $2) RETURNING id`,
		userID, string(name),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("no se pudo crear la conversación: %w", err)
	}
	return id, nil
}

// ============================================================
// Tipos del endpoint /ask
// ============================================================

type AskRequest struct {
	Question       string  `json:"question"`
	UserID         string  `json:"user_id"`
	ConversationID string `json:"conversation_id"`
	Verbose        bool    `json:"verbose,omitempty"`
}

type AskResponse struct {
	Question     string                   `json:"question"`
	Intent       string                   `json:"intent"`
	IntentReason string                   `json:"intent_reason,omitempty"`
	IntentMethod string                   `json:"intent_method,omitempty"`
	Answer       string                   `json:"answer"`
	Status       string                   `json:"status"`
	Sources      []map[string]interface{} `json:"sources,omitempty"`
	SQL          interface{}              `json:"sql,omitempty"`
	Tokens       map[string]int           `json:"tokens,omitempty"`
	LatencyMs    int                      `json:"latency_ms,omitempty"`
}

// ============================================================
// Ask — POST /ask
// ============================================================

func (s *ChatbotService) Ask(
	ctx context.Context,
	userID string,
	req AskRequest,
) (*AskResponse, error) {
	req.UserID = userID
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("payload inválido: %w", err)
	}

	url := fmt.Sprintf("%s/ask", s.baseURL)

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, url, bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("no se pudo construir la petición al servicio de IA generativa: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if userID != "" {
		httpReq.Header.Set("X-User-ID", userID)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("no se pudo contactar al servicio de IA generativa: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la respuesta del servicio de IA generativa: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, &PredictionUpstreamError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}

	var result AskResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("respuesta inválida del servicio de IA generativa: %w", err)
	}
	return &result, nil
}

// ============================================================
// ChatChunk + StreamChat — listos para cuando exista /chat/stream
// ============================================================

// ChatChunk representa un fragmento de la respuesta que el servicio
// de IA generativa va produciendo (token a token o por bloques).
type ChatChunk struct {
	Content string `json:"content"`
	Done    bool   `json:"done"`
}

// streamHTTPClient no tiene timeout global: la duración de una respuesta
// de chat es impredecible, así que el corte se controla con el context.
var streamHTTPClient = &http.Client{}

// StreamChat envía el mensaje del usuario al endpoint de streaming (SSE)
// del servicio de IA generativa y va invocando onChunk por cada fragmento.
//
// NOTA: el orquestador Python actual todavía NO expone /chat/stream;
// solo /ask. Este método queda listo para cuando se implemente.
func (s *ChatbotService) StreamChat(
	ctx context.Context,
	payload map[string]interface{},
	onChunk func(ChatChunk) error,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("payload inválido: %w", err)
	}

	url := fmt.Sprintf("%s/chat/stream", s.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("no se pudo construir la petición al servicio de IA generativa: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := streamHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo contactar al servicio de IA generativa: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return &PredictionUpstreamError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	reader := bufio.NewReader(resp.Body)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("error leyendo el stream del servicio de IA generativa: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return onChunk(ChatChunk{Done: true})
		}

		var chunk ChatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			chunk = ChatChunk{Content: data}
		}

		if err := onChunk(chunk); err != nil {
			return err
		}
	}
}

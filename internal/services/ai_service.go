package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ---------- Tipos del endpoint /ask de FastAPI ----------

type AskRequest struct {
	Question string `json:"question"`
	Verbose  bool   `json:"verbose,omitempty"`
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

var askHTTPClient = &http.Client{
	Timeout: 120 * time.Second,
}

func (ai *AIService) Ask(
	ctx context.Context,
	userID string,
	req AskRequest,
) (*AskResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("payload inválido: %w", err)
	}

	url := fmt.Sprintf("%s/ask", ai.baseURL)

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		url,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("no se pudo construir la petición al servicio de IA: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if userID != "" {
		httpReq.Header.Set("X-User-ID", userID)
	}

	resp, err := askHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("no se pudo contactar al servicio de IA: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la respuesta del servicio de IA: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, &PredictionUpstreamError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}

	var result AskResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("respuesta inválida del servicio de IA: %w", err)
	}
	return &result, nil
}

type AIService struct {
	baseURL    string
	httpClient *http.Client
}

func NewAIService(baseURL string) *AIService {
	return &AIService{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Tipos soportados por la API de IA (src/api/main.py del servicio de predicción).
var allowedPredictionTypes = map[string]bool{
	"ipm":                   true,
	"privacion":             true,
	"porcentaje":            true,
	"dimension":             true,
	"incidence_by_sex":      true,
	"incidence_by_head_sex": true,
	"contribution_latam":    true,
	"national_poverty":      true,
	"poverty_by_age":        true,
	"department_poverty":    true,
	"multiple_departments":  true,
	"household_poverty":     true,
	"multiple_households":   true,
}

var ErrPredictionTypeNotAllowed = errors.New("tipo de predicción no permitido")

type PredictionUpstreamError struct {
	StatusCode int
	Body       string
}

func (e *PredictionUpstreamError) Error() string {
	return fmt.Sprintf("el servicio de IA respondió con error (%d): %s", e.StatusCode, e.Body)
}

func (ai *AIService) Predict(predictionType string, payload map[string]interface{}) (map[string]interface{}, error) {
	if !allowedPredictionTypes[predictionType] {
		return nil, ErrPredictionTypeNotAllowed
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload inválido: %w", err)
	}

	url := fmt.Sprintf("%s/predict/%s", ai.baseURL, predictionType)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("no se pudo construir la petición al servicio de IA: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ai.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudo contactar al servicio de IA: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la respuesta del servicio de IA: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, &PredictionUpstreamError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("respuesta inválida del servicio de IA: %w", err)
	}

	return result, nil
}

// ExportPDF reenvía un conjunto de datos (título + registros) al servicio de IA
// para que genere el reporte y devuelve el PDF crudo tal cual, sin intentar
// decodificarlo como JSON.
func (ai *AIService) ExportPDF(payload map[string]interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload inválido: %w", err)
	}

	url := fmt.Sprintf("%s/export/pdf", ai.baseURL)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("no se pudo construir la petición al servicio de IA: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ai.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudo contactar al servicio de IA: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la respuesta del servicio de IA: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, &PredictionUpstreamError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	return respBody, nil
}

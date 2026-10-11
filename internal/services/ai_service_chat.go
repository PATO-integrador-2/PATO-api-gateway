package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ChatChunk representa un fragmento de la respuesta que el servicio de IA
// va generando (token a token o por bloques) para el chatbot.
type ChatChunk struct {
	Content string `json:"content"`
	Done    bool   `json:"done"`
}

// streamHTTPClient no tiene timeout global: la duración de una respuesta de
// chat es impredecible, así que el corte se controla con el context (ctx)
// que recibe StreamChat, no con un timeout fijo como en ai.httpClient.
var streamHTTPClient = &http.Client{}

// StreamChat envía el mensaje del usuario al endpoint de streaming (SSE) del
// servicio de IA y va invocando onChunk por cada fragmento recibido, en
// tiempo real. Si ctx se cancela (p. ej. el cliente cerró el WebSocket),
// la petición HTTP al backend de IA se corta también.
//
// Asume que el backend expone algo como:
//
//	POST {baseURL}/chat/stream
//	Content-Type: application/json
//	Accept: text/event-stream
//
// y responde con líneas "data: {...}\n\n", terminando con "data: [DONE]\n\n".
// Ajusta el path y el parseo si tu backend usa otro formato.
func (ai *AIService) StreamChat(ctx context.Context, payload map[string]interface{}, onChunk func(ChatChunk) error) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("payload inválido: %w", err)
	}

	url := fmt.Sprintf("%s/chat/stream", ai.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("no se pudo construir la petición al servicio de IA: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := streamHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo contactar al servicio de IA: %w", err)
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
			return fmt.Errorf("error leyendo el stream del servicio de IA: %w", err)
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
			// Si el backend no manda JSON estructurado, se trata el
			// contenido crudo como texto plano del fragmento.
			chunk = ChatChunk{Content: data}
		}

		if err := onChunk(chunk); err != nil {
			return err
		}
	}
}

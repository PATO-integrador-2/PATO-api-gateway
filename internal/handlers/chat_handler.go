package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"backend/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// ChatHandler gestiona la conexión WebSocket del chatbot: valida el token,
// recibe mensajes del cliente y devuelve la respuesta del servicio de IA
// generativa (orquestador /ask).
type ChatHandler struct {
	chatbotService *services.ChatbotService
	authService    *services.AuthService
	upgrader       websocket.Upgrader
}

func NewChatHandler(
	chatbotService *services.ChatbotService,
	authService *services.AuthService,
	allowedOrigin string,
) *ChatHandler {
	return &ChatHandler{
		chatbotService: chatbotService,
		authService:    authService,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				// Sin header Origin (clientes no-browser) se permite;
				// si viene, debe coincidir con el origen configurado.
				return origin == "" || allowedOrigin == "*" || origin == allowedOrigin
			},
		},
	}
}

type incomingMessage struct {
	ConversationID *string `json:"conversationId"` // null = conversación nueva
	Message        string `json:"message"`
}

type outgoingMessage struct {
	Type           string `json:"type"` // "chunk" | "done" | "error"
	ConversationID string `json:"conversationId,omitempty"`
	Content        string `json:"content,omitempty"`
	Error          string `json:"error,omitempty"`
}

// HandleChat hace el upgrade a WebSocket y sirve la conversación completa
// mientras la conexión esté abierta (varios mensajes, uno tras otro).
//
// El token se recibe como query param (?token=...) porque el WebSocket
// nativo del navegador no permite headers personalizados en el handshake.
func (h *ChatHandler) HandleChat(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "token requerido",
			"code":  "UNAUTHORIZED",
		})
		return
	}

	userID, err := h.authService.ValidateToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "token inválido o expirado",
			"code":  "UNAUTHORIZED",
		})
		return
	}

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("error al hacer upgrade a websocket: %v", err)
		return
	}
	defer conn.Close()

	// gorilla/websocket no permite escrituras concurrentes en la misma
	// conexión: los chunks de la IA y los pings de keepalive comparten
	// este mutex.
	var writeMu sync.Mutex
	writeJSON := func(msg outgoingMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(msg)
	}

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	go pingLoop(ctx, conn, &writeMu)

	for {
		// 1. Leer el mensaje entrante del cliente
		var in incomingMessage
		if err := conn.ReadJSON(&in); err != nil {
			if websocket.IsUnexpectedCloseError(
				err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
			) {
				log.Printf("websocket cerrado inesperadamente (user=%s): %v", userID, err)
			}
			return
		}

		// 2. Ignorar mensajes vacíos
		if strings.TrimSpace(in.Message) == "" {
			_ = writeJSON(outgoingMessage{
				Type:  "error",
				Error: "mensaje vacío",
			})
			continue
		}

		// 3. Llamar a la IA generativa (POST /ask)
		conversationID := ""
		if in.ConversationID != nil {
			conversationID = strings.TrimSpace(*in.ConversationID)
		}
		if conversationID == "" {
			conversationID, err = h.chatbotService.CreateConversation(ctx, userID, in.Message)
			if err != nil {
				log.Printf("error creando conversación (user=%s): %v", userID, err)
				_ = writeJSON(outgoingMessage{
					Type:  "error",
					Error: "no se pudo crear la conversación",
				})
				continue
			}
		}

		askReq := services.AskRequest{
			Question:       in.Message,
			ConversationID: conversationID,
			Verbose:        false,
		}

		result, err := h.chatbotService.Ask(ctx, userID, askReq)
		if err != nil {
			log.Printf("error llamando a IA generativa (user=%s): %v", userID, err)
			_ = writeJSON(outgoingMessage{
				Type:  "error",
				Error: "no se pudo generar la respuesta",
			})
			continue
		}

		// 4. Mandar la respuesta completa como un solo chunk
		if err := writeJSON(outgoingMessage{
			Type:           "chunk",
			ConversationID: conversationID,
			Content:        result.Answer,
		}); err != nil {
			return
		}

		// 5. Cerrar con done
		if err := writeJSON(outgoingMessage{
			Type:           "done",
			ConversationID: conversationID,
		}); err != nil {
			return
		}
	}
}

type askBody struct {
	ConversationID string `json:"conversation_id"` // vacío = conversación nueva
	Question       string `json:"question"`
	Verbose        bool   `json:"verbose"`
}

// Ask es la versión HTTP (sin WebSocket) del chat: POST /api/users/chat/ask.
// El user_id sale siempre del token validado por el gateway.
func (h *ChatHandler) Ask(c *gin.Context) {
	userID := c.GetString("userID")

	var body askBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos: se requiere 'question'"})
		return
	}
	if strings.TrimSpace(body.Question) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "La pregunta no puede estar vacía"})
		return
	}

	ctx := c.Request.Context()
	conversationID := strings.TrimSpace(body.ConversationID)
	if conversationID == "" {
		var err error
		conversationID, err = h.chatbotService.CreateConversation(ctx, userID, body.Question)
		if err != nil {
			log.Printf("error creando conversación (user=%s): %v", userID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo crear la conversación"})
			return
		}
	}

	result, err := h.chatbotService.Ask(ctx, userID, services.AskRequest{
		Question:       body.Question,
		ConversationID: conversationID,
		Verbose:        body.Verbose,
	})
	if err != nil {
		log.Printf("error llamando a IA generativa (user=%s): %v", userID, err)
		var upstream *services.PredictionUpstreamError
		if errors.As(err, &upstream) && upstream.StatusCode >= 400 && upstream.StatusCode <= 599 {
			c.JSON(upstream.StatusCode, gin.H{"error": "El servicio de IA no pudo procesar la pregunta"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "No se pudo contactar al servicio de IA"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"conversation_id": conversationID,
		"answer":          result.Answer,
		"intent":          result.Intent,
		"status":          result.Status,
		"sources":         result.Sources,
		"tokens":          result.Tokens,
		"latency_ms":      result.LatencyMs,
	})
}

// pingLoop mantiene la conexión viva enviando pings periódicos; termina
// cuando ctx se cancela (cierre del handler) o falla la escritura.
func pingLoop(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

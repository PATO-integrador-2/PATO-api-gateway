package models

import (
	"encoding/json"
	"time"
)

// ConversationSummary es lo que se devuelve en el listado de conversaciones.
type ConversationSummary struct {
	ID         string    `json:"id"`
	Name       *string   `json:"name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// Message representa un mensaje individual dentro de una conversación.
type Message struct {
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Position  int             `json:"position"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

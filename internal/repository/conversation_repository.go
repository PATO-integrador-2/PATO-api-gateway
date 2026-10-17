package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"backend/internal/models"
)

var ErrNotFound = errors.New("not found")

type ConversationRepository struct {
	db *sql.DB
}

func NewConversationRepository(db *sql.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

// ListByUser devuelve las conversaciones activas de un usuario, ordenadas por
// fecha de creación descendente.
func (r *ConversationRepository) ListByUser(
	ctx context.Context,
	userID string,
	limit, offset int,
) ([]models.ConversationSummary, error) {
	const q = `
		SELECT
			c.id,
			c.name,
			c.created_at,
			COALESCE(MAX(m.created_at), c.created_at) AS last_used_at
		FROM ai_conversation c
		LEFT JOIN ai_conversation_message m
			ON m.conversation_id = c.id
			AND m.is_deleted = FALSE
		WHERE c.user_id = $1
		  AND c.is_deleted = FALSE
		GROUP BY c.id, c.name, c.created_at
		ORDER BY last_used_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.QueryContext(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	out := make([]models.ConversationSummary, 0, limit)
	for rows.Next() {
		var c models.ConversationSummary
		if err := rows.Scan(
			&c.ID,
			&c.Name,
			&c.CreatedAt,
			&c.LastUsedAt,
		); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetMessages devuelve los mensajes de una conversación validando ownership
// en la misma query (JOIN con ai_conversation).
func (r *ConversationRepository) GetMessages(
	ctx context.Context,
	conversationID, userID string,
	limit, offset int,
) ([]models.Message, error) {
	const q = `
		SELECT
			m.id,
			m.role,
			m.content,
			m.position,
			m.metadata,
			m.created_at
		FROM ai_conversation_message m
		INNER JOIN ai_conversation c
			ON c.id = m.conversation_id
		WHERE m.conversation_id = $1
		  AND c.user_id = $2
		  AND c.is_deleted = FALSE
		  AND m.is_deleted = FALSE
		ORDER BY m.position ASC
		LIMIT $3 OFFSET $4
	`

	rows, err := r.db.QueryContext(ctx, q, conversationID, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	out := make([]models.Message, 0, limit)
	for rows.Next() {
		var (
			m    models.Message
			meta []byte
		)
		if err := rows.Scan(
			&m.ID,
			&m.Role,
			&m.Content,
			&m.Position,
			&meta,
			&m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if len(meta) > 0 {
			m.Metadata = json.RawMessage(meta)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SoftDelete marca la conversación como eliminada. Solo si pertenece al usuario.
// SoftDelete marca la conversación como eliminada. Solo si pertenece al usuario.
func (r *ConversationRepository) SoftDelete(
	ctx context.Context,
	conversationID, userID string,
) error {
	const q = `
		UPDATE ai_conversation
		SET 
			is_deleted = TRUE,
			deleted_at = NOW() -- <- Aquí está la magia para cumplir el constraint
		WHERE id = $1
		  AND user_id = $2
		  AND is_deleted = FALSE
	`

	res, err := r.db.ExecContext(ctx, q, conversationID, userID)
	if err != nil {
		return fmt.Errorf("soft delete: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

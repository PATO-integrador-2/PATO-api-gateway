package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrUserNotFound se devuelve cuando el usuario no existe o fue eliminado.
var ErrUserNotFound = errors.New("usuario no encontrado")

// quotaResetKey guarda en Valkey la fecha (YYYY-MM-DD) del último reinicio
// diario, para no perderlo si el gateway estuvo caído a medianoche y para que
// solo una instancia lo ejecute.
const quotaResetKey = "quota:last_reset"

// QuotaStatus es el estado de la cuota diaria de un usuario.
type QuotaStatus struct {
	Used      int64
	Max       int64
	Unlimited bool
}

// Exhausted indica si el usuario ya consumió todos sus tokens del día.
func (q QuotaStatus) Exhausted() bool {
	return !q.Unlimited && q.Used >= q.Max
}

// QuotaService lee y actualiza la cuota diaria de tokens.
//   - Límite: role.max_tokens (el mayor entre los roles del usuario).
//   - Consumo: app_user.used_tokens.
type QuotaService struct {
	db     *sql.DB
	valkey *redis.Client
}

func NewQuotaService(db *sql.DB, valkey *redis.Client) *QuotaService {
	return &QuotaService{db: db, valkey: valkey}
}

// Status devuelve el consumo y el límite del usuario.
// Es ilimitado si algún rol tiene max_tokens NULL o si no tiene roles con límite.
func (s *QuotaService) Status(ctx context.Context, userID string) (QuotaStatus, error) {
	const query = `
		SELECT u.used_tokens,
		       lim.max_tokens,
		       COALESCE(lim.has_unlimited, false)
		FROM app_user u
		LEFT JOIN LATERAL (
			SELECT MAX(r.max_tokens)            AS max_tokens,
			       BOOL_OR(r.max_tokens IS NULL) AS has_unlimited
			FROM user_role ur
			JOIN role r ON r.id = ur.role_id
			WHERE ur.user_id = u.id
		) lim ON true
		WHERE u.id = $1 AND u.deleted_at IS NULL`

	var (
		used         int64
		maxTokens    sql.NullInt64
		hasUnlimited bool
	)

	err := s.db.QueryRowContext(ctx, query, userID).Scan(&used, &maxTokens, &hasUnlimited)
	if errors.Is(err, sql.ErrNoRows) {
		return QuotaStatus{}, ErrUserNotFound
	}
	if err != nil {
		return QuotaStatus{}, fmt.Errorf("no se pudo leer la cuota: %w", err)
	}

	return QuotaStatus{
		Used:      used,
		Max:       maxTokens.Int64,
		Unlimited: hasUnlimited || !maxTokens.Valid,
	}, nil
}

// AddUsage suma tokens al consumo del día de forma atómica y devuelve el
// nuevo total.
func (s *QuotaService) AddUsage(ctx context.Context, userID string, tokens int64) (int64, error) {
	var used int64
	err := s.db.QueryRowContext(
		ctx,
		`UPDATE app_user SET used_tokens = used_tokens + $2
		 WHERE id = $1 RETURNING used_tokens`,
		userID, tokens,
	).Scan(&used)

	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrUserNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("no se pudo registrar el consumo: %w", err)
	}

	return used, nil
}

// ResetIfNewDay reinicia used_tokens de todos los usuarios si todavía no se
// hizo hoy. SET ... GET es atómico, así que con varias instancias solo una
// ejecuta el reinicio.
func (s *QuotaService) ResetIfNewDay(ctx context.Context, now time.Time) error {
	today := now.Format("2006-01-02")

	last, err := s.valkey.SetArgs(ctx, quotaResetKey, today, redis.SetArgs{Get: true}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("valkey: %w", err)
	}
	if last == today {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE app_user SET used_tokens = 0 WHERE used_tokens <> 0`); err != nil {
		// Se libera la marca para reintentar en el siguiente ciclo.
		if last == "" {
			s.valkey.Del(ctx, quotaResetKey)
		} else {
			s.valkey.Set(ctx, quotaResetKey, last, 0)
		}
		return fmt.Errorf("no se pudo reiniciar la cuota diaria: %w", err)
	}

	log.Printf("quota: cuota diaria reiniciada (%s)", today)
	return nil
}

// StartDailyReset comprueba cada minuto si cambió el día y reinicia la cuota.
// También lo hace al arrancar, por si el gateway estuvo caído a medianoche.
func (s *QuotaService) StartDailyReset(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			if err := s.ResetIfNewDay(ctx, time.Now()); err != nil {
				log.Printf("quota: %v", err)
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// SecondsUntilReset devuelve los segundos que faltan para la próxima medianoche
// (hora local del servidor), cuando se reinicia la cuota.
func SecondsUntilReset(now time.Time) int {
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	return int(next.Sub(now).Seconds())
}

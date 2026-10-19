package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"backend/internal/models"
)

type ScraperRepository struct {
	db *sql.DB
}

func NewScraperRepository(db *sql.DB) *ScraperRepository {
	return &ScraperRepository{db: db}
}

func (r *ScraperRepository) Create(ctx context.Context, s *models.ScrapeSchedule) error {
	seedURLs, _ := json.Marshal(s.SeedURLs)
	allowedDomains, _ := json.Marshal(s.AllowedDomains)
	keywords, _ := json.Marshal(s.Keywords)

	query := `
		INSERT INTO scrape_schedules (name, cron_expression, seed_urls, allowed_domains, keywords, max_depth, max_pages, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at, updated_at`
	return r.db.QueryRowContext(ctx, query, s.Name, s.CronExpression, seedURLs, allowedDomains, keywords, s.MaxDepth, s.MaxPages, s.IsActive).
		Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
}

func (r *ScraperRepository) List(ctx context.Context) ([]models.ScrapeSchedule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, cron_expression, seed_urls, allowed_domains, keywords, max_depth, max_pages, is_active, last_run_at, created_at FROM scrape_schedules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.ScrapeSchedule
	for rows.Next() {
		var s models.ScrapeSchedule
		var seedURLs, allowedDomains, keywords []byte
		if err := rows.Scan(&s.ID, &s.Name, &s.CronExpression, &seedURLs, &allowedDomains, &keywords, &s.MaxDepth, &s.MaxPages, &s.IsActive, &s.LastRunAt, &s.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal(seedURLs, &s.SeedURLs)
		json.Unmarshal(allowedDomains, &s.AllowedDomains)
		json.Unmarshal(keywords, &s.Keywords)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ScraperRepository) GetByID(ctx context.Context, id int64) (*models.ScrapeSchedule, error) {
	var s models.ScrapeSchedule
	var seedURLs, allowedDomains, keywords []byte
	query := `SELECT id, name, cron_expression, seed_urls, allowed_domains, keywords, max_depth, max_pages, is_active FROM scrape_schedules WHERE id = $1`
	err := r.db.QueryRowContext(ctx, query, id).Scan(&s.ID, &s.Name, &s.CronExpression, &seedURLs, &allowedDomains, &keywords, &s.MaxDepth, &s.MaxPages, &s.IsActive)
	if err != nil {
		return nil, err
	}
	json.Unmarshal(seedURLs, &s.SeedURLs)
	json.Unmarshal(allowedDomains, &s.AllowedDomains)
	json.Unmarshal(keywords, &s.Keywords)
	return &s, nil
}

func (r *ScraperRepository) StartExecution(ctx context.Context, scheduleID int64) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `INSERT INTO scrape_executions (schedule_id, status) VALUES ($1, 'running') RETURNING id`, scheduleID).Scan(&id)
	return id, err
}

func (r *ScraperRepository) FinishExecution(ctx context.Context, execID int64, status, details, errStr string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scrape_executions SET status = $1, finished_at = NOW(), details = $2, error = $3 WHERE id = $4`, status, details, errStr, execID)
	return err
}

func (r *ScraperRepository) UpdateLastRun(ctx context.Context, scheduleID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scrape_schedules SET last_run_at = NOW() WHERE id = $1`, scheduleID)
	return err
}

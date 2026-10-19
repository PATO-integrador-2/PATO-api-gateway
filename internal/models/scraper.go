package models

import "time"

type ScrapeSchedule struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	CronExpression string     `json:"cron_expression"`
	SeedURLs       []string   `json:"seed_urls"`
	AllowedDomains []string   `json:"allowed_domains"`
	Keywords       []string   `json:"keywords"`
	MaxDepth       int        `json:"max_depth"`
	MaxPages       int        `json:"max_pages"`
	IsActive       bool       `json:"is_active"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

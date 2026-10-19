package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"backend/internal/models"
	"backend/internal/repository"

	"github.com/robfig/cron/v3"
)

type ScraperService struct {
	repo       *repository.ScraperRepository
	baseURL    string
	httpClient *http.Client
	cron       *cron.Cron
}

func NewScraperService(repo *repository.ScraperRepository, baseURL string) *ScraperService {
	return &ScraperService{
		repo:    repo,
		baseURL: baseURL,
		httpClient: &http.Client{
			// El pipeline en Python puede tardar horas si descarga muchos PDFs
			Timeout: 4 * time.Hour,
		},
		cron: cron.New(cron.WithLocation(time.Local)),
	}
}

// StartScheduler carga las tareas de la BD y arranca el reloj
func (s *ScraperService) StartScheduler(ctx context.Context) error {
	schedules, err := s.repo.List(ctx)
	if err != nil {
		return err
	}

	for _, sch := range schedules {
		if sch.IsActive {
			s.addCronJob(sch)
		}
	}

	s.cron.Start()
	log.Println("[ScraperService] Scheduler started")
	return nil
}

func (s *ScraperService) addCronJob(sch models.ScrapeSchedule) {
	id := sch.ID
	_, err := s.cron.AddFunc(sch.CronExpression, func() {
		log.Printf("[ScraperService] Running scheduled job ID %d\n", id)
		s.RunSchedule(context.Background(), id)
	})
	if err != nil {
		log.Printf("[ScraperService] Failed to schedule job %d: %v\n", id, err)
	}
}

// CreateSchedule registra una nueva tarea en BD y la añade al reloj
func (s *ScraperService) CreateSchedule(ctx context.Context, sch *models.ScrapeSchedule) error {
	if err := s.repo.Create(ctx, sch); err != nil {
		return err
	}
	if sch.IsActive {
		s.addCronJob(*sch)
	}
	return nil
}

func (s *ScraperService) ListSchedules(ctx context.Context) ([]models.ScrapeSchedule, error) {
	return s.repo.List(ctx)
}

// RunSchedule dispara el pipeline asíncronamente
func (s *ScraperService) RunSchedule(ctx context.Context, scheduleID int64) error {
	sch, err := s.repo.GetByID(ctx, scheduleID)
	if err != nil {
		return fmt.Errorf("schedule not found: %w", err)
	}

	execID, err := s.repo.StartExecution(ctx, scheduleID)
	if err != nil {
		return fmt.Errorf("failed to start execution: %w", err)
	}

	// Ejecutamos en segundo plano para no bloquear la petición HTTP de Gin ni el Cron
	go func() {
		bgCtx := context.Background()

		details, err := s.executePipeline(bgCtx, sch)

		status := "success"
		errStr := ""
		if err != nil {
			status = "failed"
			errStr = err.Error()
			log.Printf("[ScraperService] Execution failed for ID %d: %v\n", scheduleID, err)
		}

		s.repo.FinishExecution(bgCtx, execID, status, details, errStr)
		s.repo.UpdateLastRun(bgCtx, scheduleID)
	}()

	return nil
}

func (s *ScraperService) executePipeline(ctx context.Context, sch *models.ScrapeSchedule) (string, error) {
	// 1. Llamar a /ingestion/discover pasando la configuración
	payload := map[string]interface{}{
		"seed_urls":       sch.SeedURLs,
		"allowed_domains": sch.AllowedDomains,
		"keywords":        sch.Keywords,
		"max_depth":       sch.MaxDepth,
		"max_pages":       sch.MaxPages,
	}
	bodyBytes, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/ingestion/discover", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("discover failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("discover returned status %d", resp.StatusCode)
	}

	// Parsear los run_ids devueltos por Python
	var discoverResult struct {
		Sources map[string]struct {
			RunID string `json:"run_id"`
		} `json:"sources"`
	}
	json.NewDecoder(resp.Body).Decode(&discoverResult)

	// 2. Ejecutar download -> process -> publish por cada run
	for source, info := range discoverResult.Sources {
		if info.RunID == "" {
			continue
		}
		paths := []string{
			fmt.Sprintf("/ingestion/runs/%s/download", info.RunID),
			fmt.Sprintf("/ingestion/runs/%s/process", info.RunID),
			fmt.Sprintf("/ingestion/runs/%s/publish", info.RunID),
		}
		for _, path := range paths {
			stepReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, nil)
			stepResp, stepErr := s.httpClient.Do(stepReq)
			if stepErr != nil {
				log.Printf("[ScraperService] Step %s failed for %s: %v\n", path, source, stepErr)
			} else {
				stepResp.Body.Close()
			}
		}
	}

	// 3. Forzar ciclo del Knowledge Worker
	_ = s.postAsync(ctx, "/ingestion/knowledge/queue/run?batch=10")

	return string(bodyBytes), nil
}

func (s *ScraperService) postAsync(ctx context.Context, path string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, nil)
	resp, err := s.httpClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

package handlers

import (
	"net/http"
	"strconv"

	"backend/internal/models"
	"backend/internal/services"

	"github.com/gin-gonic/gin"
)

type ScraperHandler struct {
	service *services.ScraperService
}

func NewScraperHandler(service *services.ScraperService) *ScraperHandler {
	return &ScraperHandler{service: service}
}

type CreateScheduleRequest struct {
	Name           string   `json:"name" binding:"required"`
	CronExpression string   `json:"cron_expression" binding:"required"`
	SeedURLs       []string `json:"seed_urls" binding:"required"`
	AllowedDomains []string `json:"allowed_domains" binding:"required"`
	Keywords       []string `json:"keywords"`
	MaxDepth       int      `json:"max_depth"`
	MaxPages       int      `json:"max_pages"`
}

func (h *ScraperHandler) CreateSchedule(c *gin.Context) {
	var req CreateScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos", "details": err.Error()})
		return
	}

	if req.MaxDepth == 0 {
		req.MaxDepth = 2
	}
	if req.MaxPages == 0 {
		req.MaxPages = 200
	}

	sch := &models.ScrapeSchedule{
		Name:           req.Name,
		CronExpression: req.CronExpression,
		SeedURLs:       req.SeedURLs,
		AllowedDomains: req.AllowedDomains,
		Keywords:       req.Keywords,
		MaxDepth:       req.MaxDepth,
		MaxPages:       req.MaxPages,
		IsActive:       true,
	}

	if err := h.service.CreateSchedule(c.Request.Context(), sch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo crear el schedule"})
		return
	}

	c.JSON(http.StatusCreated, sch)
}

func (h *ScraperHandler) ListSchedules(c *gin.Context) {
	schedules, err := h.service.ListSchedules(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error obteniendo schedules"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"schedules": schedules})
}

func (h *ScraperHandler) TriggerSchedule(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	if err := h.service.RunSchedule(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Ejecución iniciada en segundo plano", "schedule_id": id})
}

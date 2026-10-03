package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/handlers"
	"backend/internal/middleware"
	"backend/internal/proxy"
	"backend/internal/services"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}

	viewService := services.NewViewService(db)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf(
			"Failed to load configuration: %v",
			err,
		)
	}

	valkeyClient, err := database.ConnectValkey(cfg)
	if err != nil {
		log.Fatalf(
			"Failed to connect to valkey: %v",
			err,
		)
	}

	// Servicio encargado de validar tokens
	authService := services.NewAuthService(
		valkeyClient,
	)

	// Proxy hacia user-service
	userProxy, err := proxy.NewReverseProxy(
		cfg.UserService.BaseURL,
	)

	if err != nil {
		log.Fatalf(
			"Failed to create user service proxy: %v",
			err,
		)
	}

	// Servicio para consumir la API de IA (predicciones)
	aiService := services.NewAIService(cfg.AIService.BaseURL)

	// Cuota diaria de tokens de la IA generativa (RAG con Qwen).
	// Lee max_tokens (role) y used_tokens (app_user) de la BD de usuarios.
	var (
		quotaService *services.QuotaService
		ragHandler   *handlers.RAGHandler
	)
	if cfg.Quota.UsersDatabaseURL != "" {
		usersDB, err := sql.Open("pgx", cfg.Quota.UsersDatabaseURL)
		if err != nil {
			log.Fatalf("Failed to open users database: %v", err)
		}
		defer usersDB.Close()

		if err := usersDB.Ping(); err != nil {
			log.Fatalf("Failed to connect to users database: %v", err)
		}

		quotaService = services.NewQuotaService(usersDB, valkeyClient)

		resetCtx, stopReset := context.WithCancel(context.Background())
		defer stopReset()
		quotaService.StartDailyReset(resetCtx)

		ragHandler = handlers.NewRAGHandler(
			cfg.RAGService.BaseURL,
			cfg.RAGService.APIKey,
			time.Duration(cfg.RAGService.TimeoutSeconds)*time.Second,
			quotaService,
			services.TokenEstimator{
				CharsPerToken:        cfg.Quota.CharsPerToken,
				PromptOverheadTokens: cfg.Quota.PromptOverheadTokens,
				TokensPerChunk:       cfg.Quota.TokensPerChunk,
				HistoryTokens:        cfg.Quota.HistoryTokens,
			},
		)
	} else {
		log.Println("USERS_DATABASE_URL no definida: la ruta /api/users/chat/ask queda deshabilitada")
	}

	router := gin.Default()

	// Middlewares generales
	router.Use(
		middleware.RequestID(),
		middleware.CORS(
			cfg.CORS.AllowedOrigin,
		),
	)

	// Health del Gateway
	router.GET("/health", func(c *gin.Context) {

		c.JSON(200, gin.H{
			"status": "healthy",
			"app":    cfg.Server.Name,
		})
	})

	// =========================
	// RUTAS PÚBLICAS
	// =========================

	viewHandler := handlers.NewViewHandler(viewService)
	aiHandler := handlers.NewAIHandler(aiService)

	publicUsers := router.Group("/api/users")
	{
		publicUsers.POST(
			"/register",
			handlers.ProxyHandler(userProxy),
		)

		publicUsers.POST(
			"/login",
			handlers.ProxyHandler(userProxy),
		)

		publicUsers.POST(
			"/verify-email",
			handlers.ProxyHandler(userProxy),
		)

		publicUsers.POST(
			"/forgot-password",
			handlers.ProxyHandler(userProxy),
		)

		publicUsers.POST(
			"/verify-reset-token",
			handlers.ProxyHandler(userProxy),
		)

		publicUsers.POST(
			"/reset-password",
			handlers.ProxyHandler(userProxy),
		)

	}

	// =========================
	// RUTAS PROTEGIDAS
	// =========================

	protectedUsers := router.Group("/api/users")

	protectedUsers.Use(
		middleware.AuthMiddleware(authService),
	)

	{
		protectedUsers.GET(
			"",
			handlers.ProxyHandler(userProxy),
		)

		protectedUsers.GET(
			"/me",
			handlers.ProxyHandler(userProxy),
		)

		protectedUsers.POST(
			"/logout",
			handlers.ProxyHandler(userProxy),
		)

		protectedUsers.PATCH(
			"/email",
			handlers.ProxyHandler(userProxy),
		)

		protectedUsers.PATCH(
			"/username",
			handlers.ProxyHandler(userProxy),
		)

		protectedUsers.PATCH(
			"/password",
			handlers.ProxyHandler(userProxy),
		)

		protectedUsers.GET(
			"/ipm-by-domain",
			handlers.ViewTotalHandler(viewService, "vw_ipm_incidence"),
		)
		protectedUsers.GET(
			"/average-deprivations",
			handlers.ViewTotalHandler(viewService, "vw_ipm_deprivation_proportion"),
		)
		protectedUsers.GET(
			"/deprivations-by-variable",
			handlers.ViewTotalHandler(viewService, "vw_ipm_deprivations_variable"),
		)

		protectedUsers.GET(
			"/national-poverty",
			handlers.ViewTotalHandler(viewService, "vw_latam_ipm_national_population"),
		)

		protectedUsers.GET(
			"/poverty-by-age",
			handlers.ViewTotalHandler(viewService, "vw_latam_ipm_age_group"),
		)

		protectedUsers.GET(
			"/deprivations-contribution",
			handlers.ViewTotalHandler(viewService, "vw_latam_ipm_deprivation_contribution"),
		)

		protectedUsers.GET(
			"/dimension-contribution",
			handlers.ViewTotalHandler(viewService, "vw_ipm_contribution_dimension"),
		)

		protectedUsers.GET(
			"/incidence-by-household-head-sex",
			handlers.ViewTotalHandler(viewService, "vw_ipm_incidence_household_head_sex"),
		)

		protectedUsers.GET(
			"/incidence-by-person-sex",
			handlers.ViewTotalHandler(viewService, "vw_ipm_incidence_person_sex"),
		)

		protectedUsers.GET(
			"/household-poverty-by-department",
			handlers.ViewTotalHandler(viewService, "vw_ipm_household_departmental"),
		)

		protectedUsers.POST(
			"/filtered",
			viewHandler.GetViewFilteredData,
		)

		// Historia: Filtrar por 2 o más categorías a la vez (ej. domain + country)
		protectedUsers.POST(
			"/filtered/multi",
			viewHandler.GetViewFilteredDataMulti,
		)

		// Historia: Filtrar por categoría IPM / Filtrar por ubicación
		// (obtiene los valores distintos de una columna para poblar los filtros)
		protectedUsers.GET(
			"/categories",
			viewHandler.GetCategories,
		)

		// Historia: Visualizar predicción (consume la API de IA)
		protectedUsers.POST(
			"/predictions/:type",
			aiHandler.Predict,
		)

		// Historia: Exportar datos de predicción a PDF (consume la API de IA)
		protectedUsers.POST(
			"/exports/pdf",
			aiHandler.ExportPDF,
		)

		// Historia: Predecir y exportar a PDF en un solo paso (consume la API de IA)
		protectedUsers.POST(
			"/exports/pdf/:type",
			aiHandler.ExportPredictionPDF,
		)

		// IA generativa (RAG con Qwen), con cuota diaria de tokens:
		// QuotaMiddleware responde 429 y no reenvía al RAG si ya se agotó.
		if ragHandler != nil {
			protectedUsers.POST(
				"/chat/ask",
				middleware.QuotaMiddleware(quotaService),
				ragHandler.Ask,
			)
		}
	}

	addr := fmt.Sprintf(
		":%s",
		cfg.Server.Port,
	)

	log.Printf(
		"Starting %s on %s",
		cfg.Server.Name,
		addr,
	)

	if err := router.Run(addr); err != nil {
		log.Fatalf(
			"Failed to start server: %v",
			err,
		)
	}

}

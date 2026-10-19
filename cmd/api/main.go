package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/handlers"
	"backend/internal/middleware"
	"backend/internal/proxy"
	"backend/internal/repository"
	"backend/internal/services"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	// ============================================================
	// 1. Base de datos principal (clean_data_db / DANE views)
	// ============================================================
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}

	// ============================================================
	// 2. Configuración
	// ============================================================
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// ============================================================
	// 3. Valkey
	// ============================================================
	valkeyClient, err := database.ConnectValkey(cfg)
	if err != nil {
		log.Fatalf("Failed to connect to valkey: %v", err)
	}

	// ============================================================
	// 4. Base de datos de usuarios (user_management_db)
	// ============================================================
	usersDBURL := cfg.UsersDB.URL
	if usersDBURL == "" {
		usersDBURL = os.Getenv("DATABASE_URL")
		log.Println("[warn] USERS_DATABASE_URL no definida; reutilizando DATABASE_URL para conversaciones")
	}

	usersDB, err := sql.Open("pgx", usersDBURL)
	if err != nil {
		log.Fatalf("Failed to open users database: %v", err)
	}
	defer usersDB.Close()

	if err := usersDB.Ping(); err != nil {
		log.Fatalf("Failed to ping users database: %v", err)
	}

	// ============================================================
	// 5. Servicios y repositorios
	// ============================================================
	viewService := services.NewViewService(db, valkeyClient)

	authService := services.NewAuthService(valkeyClient)

	userProxy, err := proxy.NewReverseProxy(cfg.UserService.BaseURL)
	if err != nil {
		log.Fatalf("Failed to create user service proxy: %v", err)
	}

	aiService := services.NewAIService(cfg.AIService.BaseURL)

	// Nota: el segundo argumento de NewChatbotService es *sql.DB.
	// Asumo usersDB porque el chatbot gestiona conversaciones.
	// Si el constructor espera la BD principal, cambia a `db`.
	chatbotService := services.NewChatbotService(
		cfg.GenerativeAI.BaseURL,
		usersDB,
	)

	chatHandler := handlers.NewChatHandler(
		chatbotService,
		authService,
		cfg.CORS.AllowedOrigin,
	)

	conversationRepo := repository.NewConversationRepository(usersDB)
	conversationHandler := handlers.NewConversationHandler(conversationRepo)

	scraperRepo := repository.NewScraperRepository(usersDB)
	scraperService := services.NewScraperService(scraperRepo, cfg.Scraper.BaseURL)

	// Arrancar el reloj de tareas (Cron)
	if err := scraperService.StartScheduler(context.Background()); err != nil {
		log.Printf("[warn] Falló al iniciar Scraper Scheduler: %v", err)
	}

	scraperHandler := handlers.NewScraperHandler(scraperService)

	// ============================================================
	// 6. Router
	// ============================================================
	router := gin.Default()

	router.Use(
		middleware.RequestID(),
		middleware.CORS(cfg.CORS.AllowedOrigin),
	)

	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "healthy",
			"app":    cfg.Server.Name,
		})
	})

	router.GET("/api/users/chat/ws", chatHandler.HandleChat)

	// ============================================================
	// 7. Rutas públicas
	// ============================================================
	viewHandler := handlers.NewViewHandler(viewService)
	aiHandler := handlers.NewAIHandler(aiService)

	publicUsers := router.Group("/api/users")
	{
		publicUsers.POST("/register", handlers.ProxyHandler(userProxy))
		publicUsers.POST("/login", handlers.ProxyHandler(userProxy))
		publicUsers.POST("/verify-email", handlers.ProxyHandler(userProxy))
		publicUsers.POST("/forgot-password", handlers.ProxyHandler(userProxy))
		publicUsers.POST("/verify-reset-token", handlers.ProxyHandler(userProxy))
		publicUsers.POST("/reset-password", handlers.ProxyHandler(userProxy))
	}

	// ============================================================
	// 8. Rutas protegidas
	// ============================================================
	protectedUsers := router.Group("/api/users")
	protectedUsers.Use(middleware.AuthMiddleware(authService))
	{
		protectedUsers.GET("", handlers.ProxyHandler(userProxy))
		protectedUsers.GET("/me", handlers.ProxyHandler(userProxy))
		protectedUsers.POST("/logout", handlers.ProxyHandler(userProxy))
		protectedUsers.PATCH("/email", handlers.ProxyHandler(userProxy))
		protectedUsers.PATCH("/username", handlers.ProxyHandler(userProxy))
		protectedUsers.PATCH("/password", handlers.ProxyHandler(userProxy))

		// Vistas IPM
		protectedUsers.GET("/ipm-by-domain",
			handlers.ViewTotalHandler(viewService, "vw_ipm_by_domain"))
		protectedUsers.GET("/average-deprivations",
			handlers.ViewTotalHandler(viewService, "vw_average_deprivations"))
		protectedUsers.GET("/deprivations-by-variable",
			handlers.ViewTotalHandler(viewService, "vw_deprivations_by_variable"))
		protectedUsers.GET("/national-poverty",
			handlers.ViewTotalHandler(viewService, "vw_dashboard03_national_poverty"))
		// Métricas que colorean el mapa (vista vw_map_metrics_payload).
		protectedUsers.GET("/map-metrics", handlers.MapMetricsHandler(viewService))
		protectedUsers.GET("/poverty-by-age",
			handlers.ViewTotalHandler(viewService, "vw_dashboard03_poverty_by_age"))
		protectedUsers.GET("/deprivations-contribution",
			handlers.ViewTotalHandler(viewService, "vw_dashboard03_deprivation_contribution"))
		protectedUsers.GET("/dimension-contribution",
			handlers.ViewTotalHandler(viewService, "vw_dimension_contribution"))
		protectedUsers.GET("/incidence-by-household-head-sex",
			handlers.ViewTotalHandler(viewService, "vw_incidence_by_household_head_sex"))
		protectedUsers.GET("/incidence-by-person-sex",
			handlers.ViewTotalHandler(viewService, "vw_incidence_by_person_sex"))
		protectedUsers.GET("/household-poverty-by-department",
			handlers.HouseholdPovertyHandler(viewService))

		protectedUsers.POST("/filtered", viewHandler.GetViewFilteredData)
		protectedUsers.POST("/filtered/multi", viewHandler.GetViewFilteredDataMulti)
		protectedUsers.GET("/categories", viewHandler.GetCategories)

		protectedUsers.POST("/predictions/:type", aiHandler.Predict)
		protectedUsers.POST("/exports/pdf", aiHandler.ExportPDF)

		// Anexo departamental del IPM (DANE), una ruta por hoja. Cada una
		// responde la lista completa y también /<ruta>/:departamento.
		departmentalRoutes := map[string]string{
			"/ipm-departments":            "ipm_departamentos",
			"/ipm-department-indicators":  "ipm_indicadores_departamento",
			"/adjusted-ipm-incidence":     "incidencia_ajustada_ipm",
			"/ipm-department-sex":         "ipm_sexo",
			"/adjusted-ipm-contributions": "contribuciones_ia_ipm",
		}

		for path, sheet := range departmentalRoutes {
			handler := handlers.DepartmentalHandler(viewService, sheet)
			protectedUsers.GET(path, handler)
			protectedUsers.GET(path+"/:departamento", handler)
		}
	}

	// ============================================================
	// 8.1 Rutas internas del Scraper (Sin Auth)
	// ============================================================
	internalRoutes := router.Group("/api/internal/scraper")
	{
		internalRoutes.POST("/schedules", scraperHandler.CreateSchedule)
		internalRoutes.GET("/schedules", scraperHandler.ListSchedules)
		internalRoutes.POST("/schedules/:id/run", scraperHandler.TriggerSchedule)
	}

	// ============================================================
	// 9. Conversaciones
	// ============================================================
	protectedConversations := router.Group("/api/conversations")
	protectedConversations.Use(middleware.AuthMiddleware(authService))
	{
		protectedConversations.GET("", conversationHandler.List)
		protectedConversations.GET("/:id/messages", conversationHandler.GetMessages)
		protectedConversations.DELETE("/:id", conversationHandler.Delete)
	}

	// ============================================================
	// 10. Arrancar
	// ============================================================
	addr := fmt.Sprintf(":%s", cfg.Server.Port)
	log.Printf("Starting %s on %s", cfg.Server.Name, addr)

	if err := router.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

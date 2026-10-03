package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Server      ServerConfig
	UserService ServiceConfig
	AIService   ServiceConfig
	RAGService  RAGConfig
	Quota       QuotaConfig
	Valkey      ValkeyConfig
	CORS        CORSConfig
}

// RAGConfig: servicio de IA generativa (Qwen vía RAG).
type RAGConfig struct {
	BaseURL        string
	APIKey         string
	TimeoutSeconds int
}

// QuotaConfig: cuota diaria de tokens por usuario.
// Los tokens se estiman en el gateway porque el RAG no los reporta.
type QuotaConfig struct {
	// BD que contiene app_user, user_role y role (user_management_db).
	UsersDatabaseURL string
	// Caracteres por token usados para estimar texto (aprox. 4 en español).
	CharsPerToken int
	// Tokens fijos de la plantilla de instrucciones del prompt del RAG.
	PromptOverheadTokens int
	// Tokens aproximados por fragmento recuperado (chunks_used).
	TokensPerChunk int
	// Tokens aproximados de historial cuando la petición continúa una conversación.
	HistoryTokens int
}

type ServerConfig struct {
	Port        string
	Environment string
	Name        string
}

type ServiceConfig struct {
	BaseURL string
}

type ValkeyConfig struct {
	Addr     string
	Username string
	Password string
	DB       int
}

type CORSConfig struct {
	AllowedOrigin string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		Server: ServerConfig{
			Port:        getEnv("APP_PORT", "8080"),
			Environment: getEnv("APP_ENV", "development"),
			Name:        getEnv("APP_NAME", "PATO API Gateway"),
		},

		UserService: ServiceConfig{
			BaseURL: getEnv(
				"USER_SERVICE_URL",
				"http://localhost:8081",
			),
		},

		AIService: ServiceConfig{
			BaseURL: getEnv(
				"AI_SERVICE_URL",
				"http://localhost:8000",
			),
		},

		RAGService: RAGConfig{
			BaseURL:        getEnv("RAG_SERVICE_URL", "http://localhost:8001"),
			APIKey:         getEnv("RAG_API_KEY", ""),
			TimeoutSeconds: getEnvInt("RAG_TIMEOUT_SECONDS", 120),
		},

		Quota: QuotaConfig{
			UsersDatabaseURL:     getEnv("USERS_DATABASE_URL", ""),
			CharsPerToken:        getEnvInt("QUOTA_CHARS_PER_TOKEN", 4),
			PromptOverheadTokens: getEnvInt("QUOTA_PROMPT_OVERHEAD_TOKENS", 250),
			TokensPerChunk:       getEnvInt("QUOTA_TOKENS_PER_CHUNK", 400),
			HistoryTokens:        getEnvInt("QUOTA_HISTORY_TOKENS", 800),
		},

		Valkey: ValkeyConfig{
			Addr:    getEnv("APP_VALKEY_ADDR", "localhost:6379"),
			Username: getEnv("APP_VALKEY_USER", ""),
			Password: getEnv("APP_VALKEY_PASSWORD", ""),
			DB:       getEnvInt("APP_VALKEY_DB", 0),
		},

		CORS: CORSConfig{
			AllowedOrigin: getEnv(
				"APP_CORS_ORIGIN",
				"http://localhost:3000",
			),
		},
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}

	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}

	return defaultValue
}

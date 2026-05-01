package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port                   string
	HFAPIKey               string
	HFBaseURL              string
	GoogleStudioAPIKey     string
	GoogleStudioBaseURL    string
	TavilyAPIKey           string
	TavilyMCPURL           string
	CloudinaryCloudName    string
	CloudinaryAPIKey       string
	CloudinaryAPISecret    string
	CloudinaryUploadFolder string
	FirebaseProjectID      string
	AllowedOrigins         []string
	RequestTimeout         time.Duration
	StreamTimeout          time.Duration
	MaxPromptChars         int
	MaxUploadBytes         int64
	MaxPDFUploadBytes      int64
	MaxPDFTextChars        int
	RateLimitPerMinute     int
	LogLevel               string
}

func Load() (Config, error) {
	cfg := Config{
		Port:                   getEnv("PORT", "8080"),
		HFAPIKey:               os.Getenv("HF_API_KEY"),
		HFBaseURL:              strings.TrimRight(getEnv("HF_ROUTER_BASE_URL", "https://router.huggingface.co/v1"), "/"),
		GoogleStudioAPIKey:     os.Getenv("GOOGLE_STUDIO_API_KEY"),
		GoogleStudioBaseURL:    strings.TrimRight(getEnv("GOOGLE_STUDIO_BASE_URL", "https://generativelanguage.googleapis.com/v1beta"), "/"),
		TavilyAPIKey:           os.Getenv("TAVILY_API_KEY"),
		TavilyMCPURL:           getEnv("TAVILY_MCP_URL", "https://mcp.tavily.com/mcp/"),
		CloudinaryCloudName:    os.Getenv("CLOUDINARY_CLOUD_NAME"),
		CloudinaryAPIKey:       os.Getenv("CLOUDINARY_API_KEY"),
		CloudinaryAPISecret:    os.Getenv("CLOUDINARY_API_SECRET"),
		CloudinaryUploadFolder: getEnv("CLOUDINARY_UPLOAD_FOLDER", "chat-ui/kotlin"),
		FirebaseProjectID:      os.Getenv("FIREBASE_PROJECT_ID"),
		AllowedOrigins:         splitCSV(getEnv("ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:5173")),
		RequestTimeout:         time.Duration(getInt("REQUEST_TIMEOUT_SECONDS", 60)) * time.Second,
		StreamTimeout:          time.Duration(getInt("STREAM_TIMEOUT_SECONDS", 180)) * time.Second,
		MaxPromptChars:         getInt("MAX_PROMPT_CHARS", 0),
		MaxUploadBytes:         int64(getInt("MAX_UPLOAD_MB", 20)) << 20,
		MaxPDFUploadBytes:      int64(getInt("MAX_PDF_UPLOAD_MB", 20)) << 20,
		MaxPDFTextChars:        getInt("MAX_PDF_TEXT_CHARS", 60000),
		RateLimitPerMinute:     getInt("RATE_LIMIT_PER_MINUTE", 30),
		LogLevel:               getEnv("LOG_LEVEL", "info"),
	}

	if cfg.HFAPIKey == "" {
		return cfg, errors.New("HF_API_KEY is required")
	}
	if cfg.FirebaseProjectID == "" {
		return cfg, errors.New("FIREBASE_PROJECT_ID is required")
	}
	if cfg.RateLimitPerMinute < 1 {
		return cfg, errors.New("RATE_LIMIT_PER_MINUTE must be greater than zero")
	}
	if cfg.MaxPromptChars < 0 {
		return cfg, errors.New("MAX_PROMPT_CHARS must be zero or greater")
	}
	if cfg.MaxUploadBytes < 1 {
		return cfg, errors.New("MAX_UPLOAD_MB must be greater than zero")
	}
	if cfg.MaxPDFUploadBytes < 1 {
		return cfg, errors.New("MAX_PDF_UPLOAD_MB must be greater than zero")
	}
	if cfg.MaxPDFTextChars < 1000 {
		return cfg, errors.New("MAX_PDF_TEXT_CHARS must be at least 1000")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

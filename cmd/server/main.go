package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	firebase "firebase.google.com/go/v4"
	"github.com/go-chi/chi/v5"

	"chat-ui-go-backend/internal/auth"
	"chat-ui-go-backend/internal/config"
	"chat-ui-go-backend/internal/handlers"
	"chat-ui-go-backend/internal/hf"
	appmiddleware "chat-ui-go-backend/internal/middleware"
	"chat-ui-go-backend/internal/store"
	"chat-ui-go-backend/internal/types"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	ctx := context.Background()
	firebaseApp, err := firebase.NewApp(ctx, &firebase.Config{
		ProjectID: cfg.FirebaseProjectID,
	})
	if err != nil {
		log.Fatalf("firebase init failed: %v", err)
	}

	authClient, err := firebaseApp.Auth(ctx)
	if err != nil {
		log.Fatalf("firebase auth init failed: %v", err)
	}

	firestoreClient, err := firebaseApp.Firestore(ctx)
	if err != nil {
		log.Fatalf("firestore init failed: %v", err)
	}
	defer firestoreClient.Close()

	hfClient := hf.NewClient(cfg.HFAPIKey, cfg.HFBaseURL, cfg.RequestTimeout)
	usageStore := store.NewFirestoreStore(firestoreClient)
	authMiddleware := auth.NewMiddleware(authClient)
	rateLimiter := appmiddleware.NewRateLimiter(cfg.RateLimitPerMinute)

	chatHandler := handlers.NewChatHandler(
		hfClient,
		usageStore,
		cfg.MaxPromptChars,
		cfg.MaxPDFUploadBytes,
		cfg.MaxPDFTextChars,
		cfg.RequestTimeout,
		cfg.StreamTimeout,
	)
	modelsHandler := handlers.NewModelsHandler(hfClient, cfg.RequestTimeout)
	googleHandler := handlers.NewGoogleHandler(cfg.GoogleStudioAPIKey, cfg.GoogleStudioBaseURL, cfg.RequestTimeout, cfg.StreamTimeout)
	tavilyHandler := handlers.NewTavilyHandler(cfg.TavilyAPIKey, cfg.TavilyMCPURL, cfg.RequestTimeout)
	cloudinaryHandler := handlers.NewCloudinaryHandler(
		cfg.CloudinaryCloudName,
		cfg.CloudinaryAPIKey,
		cfg.CloudinaryAPISecret,
		cfg.CloudinaryUploadFolder,
		cfg.MaxUploadBytes,
		cfg.RequestTimeout,
	)

	router := chi.NewRouter()
	router.Use(appmiddleware.RequestID)
	router.Use(appmiddleware.SecurityHeaders)
	router.Use(appmiddleware.CORS(cfg.AllowedOrigins))
	router.Use(recoverJSON)

	router.Get("/healthz", handlers.Health)
	router.Get("/healthz/", handlers.Health)
	router.Get("/v1/healthz", handlers.Health)

	router.Route("/v1", func(r chi.Router) {
		r.Use(authMiddleware.RequireFirebaseAuth)
		r.Use(rateLimiter.Middleware)
		r.Get("/models", modelsHandler.ServeHTTP)
		r.Post("/chat", chatHandler.Chat)
		r.Post("/chat/stream", chatHandler.Stream)
		r.Post("/chat/with-file", chatHandler.ChatWithFile)
		r.HandleFunc("/google/*", googleHandler.Proxy)
		r.Post("/mcp/tavily", tavilyHandler.MCP)
		r.Post("/cloudinary/upload", cloudinaryHandler.Upload)
	})

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.RequestTimeout + 10*time.Second,
		WriteTimeout:      cfg.StreamTimeout + 10*time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("chat backend listening on :%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown failed: %v", err)
	}
}

func recoverJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// لا نرجع stack trace للمستخدم حتى لا نكشف تفاصيل داخلية.
				types.WriteError(w, r, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Package main Swagger Aggregator Service
//
//	@title			Novel Server API Documentation
//	@version		1.0
//	@description	Централизованная документация для всех микросервисов Novel Server
//	@termsOfService	http://swagger.io/terms/
//
//	@contact.name	API Support
//	@contact.url	http://www.swagger.io/support
//	@contact.email	support@swagger.io
//
//	@license.name	Apache 2.0
//	@license.url	http://www.apache.org/licenses/LICENSE-2.0.html
//
//	@host		localhost:28960
//	@BasePath	/api/v1
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Type "Bearer" followed by a space and JWT token.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"

	sharedLogger "novel-server/shared/logger"

	_ "novel-server/swagger-aggregator/docs"
)

type Config struct {
	Port           string
	Env            string
	AllowedOrigins []string

	// Service URLs (внутренние Docker имена)
	AuthServiceURL      string
	AdminServiceURL     string
	GameplayServiceURL  string
	WebSocketServiceURL string
}

type ServiceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	BaseURL     string `json:"base_url"`
	DocsURL     string `json:"docs_url"`
	Available   bool   `json:"available"`
}

func loadConfig() *Config {
	return &Config{
		Port:           getEnv("PORT", "8090"),
		Env:            getEnv("ENV", "development"),
		AllowedOrigins: strings.Split(getEnv("ALLOWED_ORIGINS", "*"), ","),

		// Используем внутренние Docker имена сервисов
		AuthServiceURL:      getEnv("AUTH_SERVICE_URL", "http://auth-service:8081"),
		AdminServiceURL:     getEnv("ADMIN_SERVICE_URL", "http://admin-service:8084"),
		GameplayServiceURL:  getEnv("GAMEPLAY_SERVICE_URL", "http://gameplay-service:8082"),
		WebSocketServiceURL: getEnv("WEBSOCKET_SERVICE_URL", "http://websocket-service:8083"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	_ = godotenv.Load()

	cfg := loadConfig()

	logger, err := sharedLogger.New(sharedLogger.Config{
		Level:    getEnv("LOG_LEVEL", "info"),
		Encoding: "json",
	})
	if err != nil {
		log.Fatalf("Не удалось инициализировать логгер: %v", err)
	}
	defer logger.Sync()

	logger.Info("Запуск Swagger Aggregator...", zap.String("port", cfg.Port))

	gin.SetMode(gin.ReleaseMode)
	if cfg.Env == "development" {
		gin.SetMode(gin.DebugMode)
	}

	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	// CORS configuration
	corsConfig := cors.DefaultConfig()
	if cfg.AllowedOrigins[0] == "*" {
		corsConfig.AllowAllOrigins = true
	} else {
		corsConfig.AllowOrigins = cfg.AllowedOrigins
	}
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Internal-Token"}
	corsConfig.AllowCredentials = true
	corsConfig.MaxAge = 12 * time.Hour
	router.Use(cors.New(corsConfig))

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "swagger-aggregator"})
	})

	// Swagger UI с кастомной конфигурацией
	setupSwaggerRoutes(router, cfg, logger)

	// API endpoints
	setupAPIRoutes(router, cfg, logger)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("Swagger Aggregator запущен", zap.String("address", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Ошибка запуска сервера", zap.Error(err))
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Получен сигнал завершения...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("Ошибка при graceful shutdown", zap.Error(err))
	}

	logger.Info("Swagger Aggregator остановлен")
}

func setupSwaggerRoutes(router *gin.Engine, cfg *Config, logger *zap.Logger) {
	// Кастомный обработчик Swagger UI с поддержкой динамического URL
	router.GET("/swagger/*any", func(c *gin.Context) {
		// Получаем URL из query параметров
		specURL := c.Query("url")
		if specURL == "" {
			// По умолчанию используем агрегированную документацию
			specURL = "/api/docs/swagger.json"
		}

		// Создаем обработчик с динамическим URL
		handler := ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL(specURL))
		handler(c)
	})

	// Главная страница с информацией о сервисах
	router.GET("/", func(c *gin.Context) {
		services := getServicesInfo(cfg, logger)
		c.HTML(http.StatusOK, "index.html", gin.H{
			"title":    "Novel Server API Documentation",
			"services": services,
		})
	})

	// Маршрут для /docs - перенаправляем на главную страницу
	router.GET("/docs", func(c *gin.Context) {
		services := getServicesInfo(cfg, logger)
		c.HTML(http.StatusOK, "index.html", gin.H{
			"title":    "Novel Server API Documentation",
			"services": services,
		})
	})

	// Маршрут для /docs/ - перенаправляем на главную страницу
	router.GET("/docs/", func(c *gin.Context) {
		services := getServicesInfo(cfg, logger)
		c.HTML(http.StatusOK, "index.html", gin.H{
			"title":    "Novel Server API Documentation",
			"services": services,
		})
	})

	// Загружаем HTML шаблоны
	router.LoadHTMLGlob("templates/*")
	router.Static("/static", "./static")

	logger.Info("Swagger UI настроен на /swagger/index.html")
}

func setupAPIRoutes(router *gin.Engine, cfg *Config, logger *zap.Logger) {
	api := router.Group("/api")
	{
		// Агрегированная документация
		api.GET("/docs/swagger.json", func(c *gin.Context) {
			aggregatedSpec := createAggregatedOpenAPISpec(cfg, logger)
			c.JSON(http.StatusOK, aggregatedSpec)
		})

		// Информация о сервисах
		api.GET("/services", func(c *gin.Context) {
			services := getServicesInfo(cfg, logger)
			c.JSON(http.StatusOK, gin.H{"services": services})
		})

		// Прокси для отдельных сервисов
		docs := api.Group("/docs")
		{
			docs.GET("/auth/swagger.json", createServiceProxyHandler(cfg.AuthServiceURL+"/api/openapi.json", logger))
			docs.GET("/admin/swagger.json", createServiceProxyHandler(cfg.AdminServiceURL+"/api/openapi.json", logger))
			docs.GET("/gameplay/swagger.json", createServiceProxyHandler(cfg.GameplayServiceURL+"/api/openapi.json", logger))
			docs.GET("/websocket/swagger.json", createServiceProxyHandler(cfg.WebSocketServiceURL+"/api/openapi.json", logger))
		}
	}
}

func createServiceProxyHandler(targetURL string, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(targetURL)
		if err != nil {
			logger.Error("Ошибка получения OpenAPI spec", zap.String("url", targetURL), zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Service unavailable"})
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Error("Ошибка чтения ответа", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
			return
		}

		c.Header("Content-Type", "application/json")
		c.Data(resp.StatusCode, "application/json", body)
	}
}

func getServicesInfo(cfg *Config, logger *zap.Logger) []ServiceInfo {
	services := []ServiceInfo{
		{
			Name:        "Auth Service",
			Description: "Аутентификация и авторизация пользователей",
			BaseURL:     "/api/v1/auth",
			DocsURL:     "/api/docs/auth/swagger.json",
		},
		{
			Name:        "Admin Service",
			Description: "Административная панель",
			BaseURL:     "/admin",
			DocsURL:     "/api/docs/admin/swagger.json",
		},
		{
			Name:        "Gameplay Service",
			Description: "Игровая логика и управление историями",
			BaseURL:     "/api/v1",
			DocsURL:     "/api/docs/gameplay/swagger.json",
		},
		{
			Name:        "WebSocket Service",
			Description: "Real-time уведомления",
			BaseURL:     "/ws",
			DocsURL:     "/api/docs/websocket/swagger.json",
		},
	}

	// Проверяем доступность сервисов
	client := &http.Client{Timeout: 5 * time.Second}
	serviceURLs := map[string]string{
		"Auth Service":      cfg.AuthServiceURL + "/health",
		"Admin Service":     cfg.AdminServiceURL + "/health",
		"Gameplay Service":  cfg.GameplayServiceURL + "/health",
		"WebSocket Service": cfg.WebSocketServiceURL + "/health",
	}

	for i, service := range services {
		if healthURL, exists := serviceURLs[service.Name]; exists {
			resp, err := client.Get(healthURL)
			if err == nil && resp.StatusCode == http.StatusOK {
				services[i].Available = true
				resp.Body.Close()
			} else {
				services[i].Available = false
				if err != nil {
					logger.Debug("Service health check failed", zap.String("service", service.Name), zap.Error(err))
				}
			}
		}
	}

	return services
}

func createAggregatedOpenAPISpec(cfg *Config, logger *zap.Logger) map[string]interface{} {
	spec := map[string]interface{}{
		"openapi": "3.0.0",
		"info": map[string]interface{}{
			"title":       "Novel Server API",
			"description": "Централизованная документация для всех микросервисов Novel Server",
			"version":     "1.0.0",
			"contact": map[string]interface{}{
				"name":  "API Support",
				"url":   "http://www.swagger.io/support",
				"email": "support@swagger.io",
			},
			"license": map[string]interface{}{
				"name": "Apache 2.0",
				"url":  "http://www.apache.org/licenses/LICENSE-2.0.html",
			},
		},
		"servers": []map[string]interface{}{
			{
				"url":         "http://localhost:28960/api/v1",
				"description": "Development Server (через Traefik)",
			},
		},
		"paths": map[string]interface{}{
			"/health": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Health check",
					"description": "Проверка состояния сервисов",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "OK",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"status": map[string]interface{}{
												"type":    "string",
												"example": "ok",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"components": map[string]interface{}{
			"securitySchemes": map[string]interface{}{
				"BearerAuth": map[string]interface{}{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
					"description":  "JWT токен для аутентификации пользователей",
				},
				"InternalAuth": map[string]interface{}{
					"type":        "apiKey",
					"in":          "header",
					"name":        "X-Internal-Token",
					"description": "Токен для межсервисного взаимодействия",
				},
			},
		},
		"x-services": map[string]interface{}{
			"auth": map[string]interface{}{
				"name":        "Auth Service",
				"description": "Аутентификация и авторизация пользователей",
				"docs_url":    "/api/docs/auth/swagger.json",
				"base_path":   "/api/v1/auth",
				"endpoints": []string{
					"POST /api/v1/auth/register",
					"POST /api/v1/auth/login",
					"POST /api/v1/auth/refresh",
					"GET /api/v1/auth/me",
				},
			},
			"admin": map[string]interface{}{
				"name":        "Admin Service",
				"description": "Административная панель",
				"docs_url":    "/api/docs/admin/swagger.json",
				"base_path":   "/admin",
				"endpoints": []string{
					"GET /admin/dashboard",
					"GET /admin/users",
					"GET /admin/stories",
				},
			},
			"gameplay": map[string]interface{}{
				"name":        "Gameplay Service",
				"description": "Игровая логика и управление историями",
				"docs_url":    "/api/docs/gameplay/swagger.json",
				"base_path":   "/api/v1",
				"endpoints": []string{
					"POST /api/v1/stories/generate",
					"GET /api/v1/stories",
					"GET /api/v1/published-stories",
					"POST /api/v1/published-stories/{id}/gamestates/{game_state_id}/choice",
				},
			},
			"websocket": map[string]interface{}{
				"name":        "WebSocket Service",
				"description": "Real-time уведомления",
				"docs_url":    "/api/docs/websocket/swagger.json",
				"base_path":   "/ws",
				"endpoints": []string{
					"GET /ws (WebSocket connection)",
				},
			},
		},
	}

	// Пытаемся получить и объединить спецификации от сервисов
	mergeServiceSpecs(spec, cfg, logger)

	return spec
}

func mergeServiceSpecs(aggregatedSpec map[string]interface{}, cfg *Config, logger *zap.Logger) {
	serviceURLs := map[string]string{
		"auth":      cfg.AuthServiceURL + "/api/openapi.json",
		"admin":     cfg.AdminServiceURL + "/api/openapi.json",
		"gameplay":  cfg.GameplayServiceURL + "/api/openapi.json",
		"websocket": cfg.WebSocketServiceURL + "/api/openapi.json",
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for serviceName, serviceURL := range serviceURLs {
		resp, err := client.Get(serviceURL)
		if err != nil {
			logger.Debug("Не удалось получить спецификацию сервиса",
				zap.String("service", serviceName),
				zap.String("url", serviceURL),
				zap.Error(err))
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logger.Debug("Сервис вернул ошибку",
				zap.String("service", serviceName),
				zap.Int("status", resp.StatusCode))
			continue
		}

		var serviceSpec map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&serviceSpec); err != nil {
			logger.Debug("Не удалось декодировать спецификацию",
				zap.String("service", serviceName),
				zap.Error(err))
			continue
		}

		// Объединяем paths из сервиса
		if servicePaths, ok := serviceSpec["paths"].(map[string]interface{}); ok {
			if aggregatedPaths, ok := aggregatedSpec["paths"].(map[string]interface{}); ok {
				for path, pathSpec := range servicePaths {
					// Добавляем префикс сервиса к пути
					prefixedPath := fmt.Sprintf("/%s%s", serviceName, path)
					aggregatedPaths[prefixedPath] = pathSpec
				}
			}
		}

		logger.Debug("Спецификация сервиса успешно объединена", zap.String("service", serviceName))
	}
}

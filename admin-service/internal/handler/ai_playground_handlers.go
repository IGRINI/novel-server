package handler

import (
	"fmt"
	"net/http"
	"novel-server/admin-service/internal/client"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

func (h *AdminHandler) handleAIPlaygroundPage(c echo.Context) error {
	data := map[string]interface{}{
		"PageTitle":  "AI Playground",
		"IsLoggedIn": true,
	}
	return c.Render(http.StatusOK, "ai_playground.html", data)
}

func (h *AdminHandler) handleAIPlaygroundGenerate(c echo.Context) error {
	systemPrompt := c.FormValue("system_prompt")
	userPrompt := c.FormValue("user_prompt")
	tempStr := c.QueryParam("temperature")
	maxTokensStr := c.QueryParam("max_tokens")
	topPStr := c.QueryParam("top_p")
	params := generationParams{
		Temperature: 0.7,
		MaxTokens:   512,
		TopP:        1.0,
	}
	var parseErrors []string
	if tempStr != "" {
		if t, err := strconv.ParseFloat(tempStr, 64); err == nil {
			if t >= 0 && t <= 2.0 {
				params.Temperature = t
			} else {
				parseErrors = append(parseErrors, "Temperature must be between 0.0 and 2.0")
			}
		} else {
			parseErrors = append(parseErrors, "Invalid Temperature format")
		}
	}
	if maxTokensStr != "" {
		if mt, err := strconv.Atoi(maxTokensStr); err == nil {
			if mt > 0 {
				params.MaxTokens = mt
			} else {
				parseErrors = append(parseErrors, "Max Tokens must be positive")
			}
		} else {
			parseErrors = append(parseErrors, "Invalid Max Tokens format")
		}
	}
	if topPStr != "" {
		if tp, err := strconv.ParseFloat(topPStr, 64); err == nil {
			if tp >= 0 && tp <= 1.0 {
				params.TopP = tp
			} else {
				parseErrors = append(parseErrors, "Top P must be between 0.0 and 1.0")
			}
		} else {
			parseErrors = append(parseErrors, "Invalid Top P format")
		}
	}
	if len(parseErrors) > 0 {
		errMsg := "Invalid generation parameters: " + strings.Join(parseErrors, ", ")
		h.logger.Warn(errMsg, zap.String("handler", "handleAIPlaygroundGenerate"))
		return echo.NewHTTPError(http.StatusBadRequest, errMsg)
	}
	log := h.logger.With(
		zap.String("handler", "handleAIPlaygroundGenerate"),
		zap.Int("systemPromptLen", len(systemPrompt)),
		zap.Int("userPromptLen", len(userPrompt)),
		zap.Float64("temperature", params.Temperature),
		zap.Int("max_tokens", params.MaxTokens),
		zap.Float64("top_p", params.TopP),
	)
	log.Info("Received AI generation request (non-streaming)")
	generationResult, err := h.storyGenClient.GenerateText(c.Request().Context(), systemPrompt, userPrompt, client.GenerationParams{
		Temperature: &params.Temperature,
		MaxTokens:   &params.MaxTokens,
		TopP:        &params.TopP,
	})
	if err != nil {
		log.Error("Failed to call story generator text API", zap.Error(err))
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Error calling generator: %v", err))
	}
	return c.String(http.StatusOK, generationResult)
}

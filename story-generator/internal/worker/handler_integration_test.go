//go:build integration

package worker_test

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novel-server/shared/messaging"
	"novel-server/story-generator/internal/config"
	"novel-server/story-generator/internal/mocks"
	"novel-server/story-generator/internal/model"
	"novel-server/story-generator/internal/service" // Импортируем реальный сервис
	"novel-server/story-generator/internal/worker"

	"github.com/joho/godotenv" // Добавляем для загрузки .env
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require" // Используем require для критичных проверок
)

// Helper function to create a temporary directory for prompts (copied from handler_test.go)
func setupIntegrationTest(t *testing.T) (string, func()) {
	tmpDir, err := os.MkdirTemp("", "prompts_int_test")
	require.NoError(t, err) // Use require for setup steps
	return tmpDir, func() { os.RemoveAll(tmpDir) }
}

// Helper function to copy prompt file without returning content
func copyPromptIntegration(t *testing.T, sourcePath, destDir, promptFilename string) {
	destPath := filepath.Join(destDir, promptFilename)

	contentBytes, err := os.ReadFile(sourcePath)
	require.NoError(t, err, "Failed to read source prompt file: %s", sourcePath) // Use require

	err = os.WriteFile(destPath, contentBytes, 0644)
	require.NoError(t, err, "Failed to write prompt file to temp dir: %s", destPath) // Use require
}

// Constants for integration tests (can reuse some from unit tests)
const (
	intTestUserID            = "int-user-789"
	intTestTaskID            = "int-task-101"
	intTestNarratorUserInput = "Сгенерируй простую историю о приключениях в фентезийном лесу, где путешественник находит заброшенный дом и встречает странного старика." // More specific input for narrator
	// Путь к файлу .env относительно файла теста (3 уровня вверх)
	dotEnvPath = "../../../.env"
	// Путь к файлу промта относительно файла теста
	intSourceNarratorPath        = "../../../promts/narrator.md"
	intSourceSetupPath           = "../../../promts/novel_setup.md"
	intSourceFirstScenePath      = "../../../promts/novel_first_scene_creator.md"
	intSourceCreatorPath         = "../../../promts/novel_creator.md"
	intSourceGameOverCreatorPath = "../../../promts/novel_gameover_creator.md"
)

// TestTaskHandler_Handle_FullGameFlow_Integration tests the entire workflow from Narrator to Game Over
func TestTaskHandler_Handle_FullGameFlow_Integration(t *testing.T) {
	// 0. Load environment variables from .env file
	err := godotenv.Load(dotEnvPath)
	if err != nil {
		// Не прерываем тест, если .env не найден, но выводим предупреждение
		// Переменные могут быть установлены иным способом (например, в CI)
		log.Printf("Warning: Could not load .env file from %s: %v\n", dotEnvPath, err)
	}

	// 1. Read required config from environment variables (loaded from .env or system env)
	apiKey := os.Getenv("AI_API_KEY")
	baseURL := os.Getenv("AI_BASE_URL") // Optional, might be empty
	modelName := os.Getenv("AI_MODEL")

	if apiKey == "" || modelName == "" {
		t.Skip("Skipping integration test: AI_API_KEY or AI_MODEL environment variables not set (checked after attempting to load .env).")
	}

	// 2. Setup test environment (prompts dir)
	promptsDir, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Copy ALL required prompt files
	copyPromptIntegration(t, intSourceNarratorPath, promptsDir, "narrator.md")
	copyPromptIntegration(t, intSourceSetupPath, promptsDir, "novel_setup.md")
	copyPromptIntegration(t, intSourceFirstScenePath, promptsDir, "novel_first_scene_creator.md")
	copyPromptIntegration(t, intSourceCreatorPath, promptsDir, "novel_creator.md")
	copyPromptIntegration(t, intSourceGameOverCreatorPath, promptsDir, "novel_gameover_creator.md")

	// 3. Create configuration
	cfg := &config.Config{
		AIAPIKey:         apiKey,
		AIBaseURL:        baseURL, // Will use default OpenAI URL if empty
		AIModel:          modelName,
		AITimeout:        300 * time.Second, // Longer timeout for real API call
		AIMaxAttempts:    1,                 // No retries in this specific test run
		AIBaseRetryDelay: 1 * time.Second,
		PromptsDir:       promptsDir,
	}

	// 4. Create REAL AI Client and MOCK Repository/Notifier
	realAIClient := service.NewAIClient(cfg) // Use the real client
	mockRepo := mocks.NewMockResultRepository(t)
	mockNotifier := mocks.NewMockNotifier(t)

	// 5. Create the handler with real AI client and mocks
	handler := worker.NewTaskHandler(cfg, realAIClient, mockRepo, mockNotifier)

	// Variables to store outputs between stages
	var narratorOutputJSON, setupOutputJSON, firstSceneOutputJSON []byte // Use []byte for raw JSON
	// var creatorOutputJSON, gameOverOutputJSON []byte // Commented out as Creator/GameOver stages are skipped/not fully implemented yet

	// -- Stage 1: Narrator --
	t.Run("NarratorStage", func(t *testing.T) {
		payload := messaging.GenerationTaskPayload{
			TaskID:     intTestTaskID + "-narrator",
			UserID:     intTestUserID,
			PromptType: messaging.PromptTypeNarrator,
			UserInput:  intTestNarratorUserInput,
			InputData:  nil,
		}

		// Configure Mocks for Narrator stage
		mockRepo.On("Save", mock.Anything, mock.MatchedBy(func(res *model.GenerationResult) bool {
			return res.ID == payload.TaskID
		})).Return(nil).Once().Run(func(args mock.Arguments) {
			result := args.Get(1).(*model.GenerationResult)
			assert.Equal(t, payload.UserID, result.UserID)
			require.Empty(t, result.Error, "Narrator stage failed with error: %s", result.Error)
			require.NotEmpty(t, result.GeneratedText, "Narrator generated empty text")
			narratorOutputJSON = []byte(result.GeneratedText) // Save raw JSON
			t.Logf("Narrator Output:\n---\n%s\n---", result.GeneratedText)
			// Add assertion for language detection (assuming Russian input)
			var narratorOutput map[string]interface{}
			errJson := json.Unmarshal(narratorOutputJSON, &narratorOutput)
			require.NoError(t, errJson, "Failed to unmarshal narrator output JSON")
			assert.Equal(t, "ru", narratorOutput["ln"], "Narrator did not detect Russian language")
		})

		mockNotifier.On("Notify", mock.Anything, mock.MatchedBy(func(notif messaging.NotificationPayload) bool {
			return notif.TaskID == payload.TaskID && notif.Status == messaging.NotificationStatusSuccess
		})).Return(nil).Once()

		err = handler.Handle(payload)
		require.NoError(t, err, "Narrator stage Handle failed")
		mockRepo.AssertExpectations(t)
		mockNotifier.AssertExpectations(t)
	})

	// -- Stage 2: Setup --
	require.NotEmpty(t, narratorOutputJSON, "Narrator output was empty, cannot proceed to Setup stage")
	t.Run("SetupStage", func(t *testing.T) {
		payload := messaging.GenerationTaskPayload{
			TaskID:     intTestTaskID + "-setup",
			UserID:     intTestUserID,
			PromptType: messaging.PromptTypeNovelSetup,
			UserInput:  string(narratorOutputJSON), // Pass Narrator JSON string as User Input
			InputData:  nil,                        // InputData is no longer used for templating here
		}

		mockRepo.On("Save", mock.Anything, mock.MatchedBy(func(res *model.GenerationResult) bool {
			return res.ID == payload.TaskID
		})).Return(nil).Once().Run(func(args mock.Arguments) {
			result := args.Get(1).(*model.GenerationResult)
			assert.Equal(t, payload.UserID, result.UserID)
			require.Empty(t, result.Error, "Setup stage failed with error: %s", result.Error)
			require.NotEmpty(t, result.GeneratedText, "Setup generated empty text")
			setupOutputJSON = []byte(strings.TrimPrefix(strings.TrimSuffix(result.GeneratedText, "\n```\n"), "\n```\n")) // Save raw JSON
			t.Logf("Setup Output:\n---\n%s\n---", result.GeneratedText)
			// Add assertions: check if core stats names match narrator output, check character count etc.
		})

		mockNotifier.On("Notify", mock.Anything, mock.MatchedBy(func(notif messaging.NotificationPayload) bool {
			return notif.TaskID == payload.TaskID && notif.Status == messaging.NotificationStatusSuccess
		})).Return(nil).Once()

		err = handler.Handle(payload)
		require.NoError(t, err, "Setup stage Handle failed")
		mockRepo.AssertExpectations(t)
		mockNotifier.AssertExpectations(t)
	})

	// -- Stage 3: First Scene Creator --
	require.NotEmpty(t, setupOutputJSON, "Setup output was empty, cannot proceed to FirstScene stage")
	t.Run("FirstSceneStage", func(t *testing.T) {
		// Combine Narrator (config) and Setup output for FirstScene input
		combinedInputMap := map[string]json.RawMessage{
			"config": narratorOutputJSON,
			"setup":  setupOutputJSON,
		}
		combinedInputJSON, errJson := json.Marshal(combinedInputMap)
		require.NoError(t, errJson, "Failed to marshal combined input for FirstScene")

		payload := messaging.GenerationTaskPayload{
			TaskID:     intTestTaskID + "-firstscene",
			UserID:     intTestUserID,
			PromptType: messaging.PromptTypeNovelFirstSceneCreator,
			UserInput:  string(combinedInputJSON), // Pass combined JSON string as User Input
			InputData:  nil,                       // InputData is no longer used for templating here
		}

		mockRepo.On("Save", mock.Anything, mock.MatchedBy(func(res *model.GenerationResult) bool {
			return res.ID == payload.TaskID
		})).Return(nil).Once().Run(func(args mock.Arguments) {
			result := args.Get(1).(*model.GenerationResult)
			assert.Equal(t, payload.UserID, result.UserID)
			require.Empty(t, result.Error, "FirstScene stage failed with error: %s", result.Error)
			require.NotEmpty(t, result.GeneratedText, "FirstScene generated empty text")
			firstSceneOutputJSON = []byte(result.GeneratedText)
			t.Logf("FirstScene Output:\n---\n%s\n---", result.GeneratedText)
			// Add assertions: check for presence of 'ch' (choices) array etc.
		})

		mockNotifier.On("Notify", mock.Anything, mock.MatchedBy(func(notif messaging.NotificationPayload) bool {
			return notif.TaskID == payload.TaskID && notif.Status == messaging.NotificationStatusSuccess
		})).Return(nil).Once()

		err = handler.Handle(payload)
		require.NoError(t, err, "FirstScene stage Handle failed")
		mockRepo.AssertExpectations(t)
		mockNotifier.AssertExpectations(t)
	})

	// -- Stage 4: Creator (Simulating one turn) --
	require.NotEmpty(t, firstSceneOutputJSON, "FirstScene output was empty, cannot proceed to Creator stage")
	t.Run("CreatorStage", func(t *testing.T) {
		// Simulate NovelState based on previous outputs (highly simplified)
		// In a real scenario, this state would be built and updated by the game engine
		var firstSceneData map[string]json.RawMessage
		errJson := json.Unmarshal(firstSceneOutputJSON, &firstSceneData)
		require.NoError(t, errJson, "Failed to unmarshal FirstScene output")

		// Simplistic state simulation
		mockState := map[string]interface{}{
			"current_stage":        "choices_ready",                                                    // Assuming we are in standard gameplay
			"language":             "ru",                                                               // Assuming from Narrator
			"core_stats":           map[string]int{"Stat1": 50, "Stat2": 50, "Stat3": 50, "Stat4": 50}, // Placeholder
			"story_summary_so_far": "Initial state from first scene...",                                // Placeholder
			"future_direction":     "Next steps...",                                                    // Placeholder
			// Add other necessary fields from NovelState based on creator prompt needs
		}
		mockStateJSON, _ := json.Marshal(mockState)

		// Combine State and Setup for Creator input
		combinedInputMap := map[string]json.RawMessage{
			"state": mockStateJSON,
			"setup": setupOutputJSON,
		}
		combinedInputJSON, errJson := json.Marshal(combinedInputMap)
		require.NoError(t, errJson, "Failed to marshal combined input for Creator")

		payload := messaging.GenerationTaskPayload{
			TaskID:     intTestTaskID + "-creator",
			UserID:     intTestUserID,
			PromptType: messaging.PromptTypeNovelCreator,
			UserInput:  string(combinedInputJSON), // Pass combined state/setup JSON string as User Input
			InputData:  nil,                       // InputData is no longer used for templating here
		}

		mockRepo.On("Save", mock.Anything, mock.MatchedBy(func(res *model.GenerationResult) bool {
			return res.ID == payload.TaskID
		})).Return(nil).Once().Run(func(args mock.Arguments) {
			result := args.Get(1).(*model.GenerationResult)
			assert.Equal(t, payload.UserID, result.UserID)
			require.Empty(t, result.Error, "Creator stage failed with error: %s", result.Error)
			require.NotEmpty(t, result.GeneratedText, "Creator generated empty text")
			// creatorOutputJSON = []byte(result.GeneratedText) // Store if needed later
			t.Logf("Creator Output:\n---\n%s\n---", result.GeneratedText)
			// Add assertions: check type field, presence of choices 'ch' etc.
		})

		mockNotifier.On("Notify", mock.Anything, mock.MatchedBy(func(notif messaging.NotificationPayload) bool {
			return notif.TaskID == payload.TaskID && notif.Status == messaging.NotificationStatusSuccess
		})).Return(nil).Once()

		err = handler.Handle(payload)
		require.NoError(t, err, "Creator stage Handle failed")
		mockRepo.AssertExpectations(t)
		mockNotifier.AssertExpectations(t)
	})

	// -- Stage 5: Game Over Creator --
	// This stage would require simulating a final state leading to game over
	// Skipping for brevity in this example, but the structure would be similar to the Creator stage,
	// passing a state with game_over conditions met and the appropriate reason.
	t.Run("GameOverStage", func(t *testing.T) {
		t.Skip("Skipping GameOver stage for brevity in this example.")
		// ... implementation similar to CreatorStage, but constructing input for game over ...
		// Need to construct a state where a core stat is <= 0 or >= 100 based on 'go' conditions.
		// Need to construct the 'reason' JSON part.
		// Pass state + setup + reason to the GameOverCreator prompt.
		// Assert the output type is 'game_over' and 'et' is present.
	})
}

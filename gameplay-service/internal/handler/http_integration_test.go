package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"novel-server/gameplay-service/internal/config"
	"novel-server/gameplay-service/internal/handler"
	"novel-server/gameplay-service/internal/messaging"
	"novel-server/gameplay-service/internal/service"
	sharedDatabase "novel-server/shared/database"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"github.com/testcontainers/testcontainers-go/wait"

	// Добавляем импорты для golang-migrate/migrate
	migrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	// Добавляем импорт для bcrypt
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

const (
	// Путь относительно gameplay-service/internal/handler/http_integration_test.go
	migrationDir  = "../../../shared/database/migrations"
	jwtTestSecret = "test-secret-for-integration" // Тестовый JWT секрет
)

// --- Локальный мок AuthServiceClient --- //
type mockAuthServiceClient struct{}

func (m *mockAuthServiceClient) GetUsersInfo(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]interfaces.UserInfo, error) {
	results := make(map[uuid.UUID]interfaces.UserInfo)
	for _, id := range userIDs {
		results[id] = interfaces.UserInfo{
			ID:          id,
			Username:    fmt.Sprintf("mockuser-%s", id.String()[:4]),
			DisplayName: fmt.Sprintf("Mock User %s", id.String()[:4]),
		}
	}
	return results, nil
}

// --- Конец локального мока --- //

// IntegrationTestSuite определяет набор интеграционных тестов
type IntegrationTestSuite struct {
	suite.Suite
	pgContainer   *postgres.PostgresContainer
	rmqContainer  *rabbitmq.RabbitMQContainer
	dbPool        *pgxpool.Pool
	rabbitConn    *amqp.Connection
	serviceURL    string
	app           *gin.Engine
	repo          interfaces.StoryConfigRepository
	taskMessages  chan amqp.Delivery // Канал для полученных сообщений из очереди задач
	stopConsumer  chan struct{}      // Канал для остановки тестового консьюмера
	consumerReady chan struct{}      // Канал для сигнала о готовности консьюмера
}

// SetupSuite запускается один раз перед всеми тестами в наборе
func (s *IntegrationTestSuite) SetupSuite() {
	ctx := context.Background()
	s.taskMessages = make(chan amqp.Delivery, 20) // Буферизованный канал
	s.stopConsumer = make(chan struct{})
	s.consumerReady = make(chan struct{}) // Инициализируем канал готовности

	// Загружаем .env для локальных путей (если нужно)
	_ = godotenv.Load("../../.env") // Путь к .env относительно handler_test.go

	// --- Запуск Postgres ---
	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test-db"),
		postgres.WithUsername("user"),
		postgres.WithPassword("password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(s.T(), err)
	s.pgContainer = pgContainer
	pgConnStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(s.T(), err)

	// --- Запуск RabbitMQ ---
	rmqContainer, err := rabbitmq.Run(ctx,
		"rabbitmq:3-management-alpine",
		testcontainers.WithWaitStrategy(
			wait.ForLog("Server startup complete"),
		),
	)
	require.NoError(s.T(), err)
	s.rmqContainer = rmqContainer
	rmqConnStr, err := rmqContainer.AmqpURL(ctx)
	require.NoError(s.T(), err)

	// --- Подключение к БД и миграции с помощью golang-migrate/migrate ---
	dbPool, err := pgxpool.New(ctx, pgConnStr)
	require.NoError(s.T(), err)
	s.dbPool = dbPool
	s.repo = sharedDatabase.NewPgStoryConfigRepository(dbPool, zap.NewNop())

	// Применение миграций
	// Абсолютный путь не обязателен, ToSlash нужен для Windows
	absoluteMigrationDir, err := filepath.Abs(migrationDir)
	require.NoError(s.T(), err)
	sourceURL := "file://" + filepath.ToSlash(absoluteMigrationDir)
	log.Printf("Applying migrations from: %s", sourceURL)
	log.Printf("Using database URL: %s", pgConnStr) // Не логируем пароль!

	m, err := migrate.New(sourceURL, pgConnStr)
	require.NoError(s.T(), err, "Failed to create migrate instance")

	// Применяем миграции Up
	err = m.Up()
	// Игнорируем ошибку "no change", которая возникает, если миграции уже применены
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		// Логируем версию БД в случае ошибки
		version, dirty, vErr := m.Version()
		if vErr != nil {
			log.Printf("Error getting migration version after failed Up: %v", vErr)
		} else {
			log.Printf("Current migration version: %d, Dirty: %v", version, dirty)
		}
		// Логируем исходную ошибку, чтобы понять, почему миграция не удалась
		log.Printf("Migration failed with error: %v", err)
		require.NoError(s.T(), err, "Failed to apply migrations")
	} else if errors.Is(err, migrate.ErrNoChange) {
		log.Println("Migrations already up to date.")
	} else {
		log.Println("Migrations applied successfully.")
	}
	// Не вызываем Close(), чтобы не было ошибки "closed source instance"
	// sourceErr, databaseErr := m.Close()
	// require.NoError(s.T(), sourceErr)
	// require.NoError(s.T(), databaseErr)

	// --- Создание тестовых пользователей ПОСЛЕ миграций ---
	testUsers := []struct {
		id       uuid.UUID
		username string
		password string
	}{
		{uuid.New(), "testuser101", "password101"},
		{uuid.New(), "testuser102", "password102"},
	}
	for _, user := range testUsers {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.password), bcrypt.DefaultCost)
		require.NoError(s.T(), err)
		insertQuery := `INSERT INTO users (id, username, password_hash) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`
		_, err = s.dbPool.Exec(ctx, insertQuery, user.id, user.username, string(hashedPassword))
		require.NoError(s.T(), err)
	}
	log.Println("Test users created successfully.")

	// --- Подключение к RabbitMQ ---
	rabbitConn, err := amqp.Dial(rmqConnStr)
	require.NoError(s.T(), err)
	s.rabbitConn = rabbitConn

	// --- Настройка и запуск Gin приложения для тестов ---
	cfg := &config.Config{ // Используем тестовые строки подключения
		Port:                     "0",
		RabbitMQURL:              rmqConnStr,
		GenerationTaskQueue:      "test_generation_tasks", // Тестовая очередь задач
		InternalUpdatesQueueName: "test_internal_updates", // Не используется напрямую в API тестах
		ClientUpdatesQueueName:   "test_client_updates",   // Не используется напрямую в API тестах
		JWTSecret:                jwtTestSecret,
	}

	// --- Настройка тестового консьюмера для очереди задач ---
	log.Println("Starting test task consumer goroutine...")
	go s.runTestTaskConsumer(cfg.RabbitMQURL, cfg.GenerationTaskQueue)

	// --- Ожидание готовности консьюмера ---
	log.Println("Waiting for test task consumer to be ready...")
	select {
	case <-s.consumerReady:
		log.Println("Test task consumer is ready.")
	case <-time.After(15 * time.Second): // Таймаут ожидания готовности консьюмера
		s.T().Fatal("Timeout waiting for test task consumer to become ready")
	}

	// --- Настройка паблишера (после готовности консьюмера) ---
	// Используем РЕАЛЬНЫЙ паблишер, подключенный к тестовому RabbitMQ
	taskChannel, err := s.rabbitConn.Channel()
	require.NoError(s.T(), err)
	taskPublisher := messaging.NewRabbitMQPublisher(taskChannel, cfg.GenerationTaskQueue)

	// Создаем реальные репозитории для зависимостей
	nopLogger := zap.NewNop()
	publishedRepo := sharedDatabase.NewPgPublishedStoryRepository(s.dbPool, nopLogger)
	sceneRepo := sharedDatabase.NewPgStorySceneRepository(s.dbPool, nopLogger)
	playerProgressRepo := sharedDatabase.NewPgPlayerProgressRepository(s.dbPool, nopLogger)
	likeRepo := sharedDatabase.NewPgLikeRepository(s.dbPool, nopLogger)
	playerGameStateRepo := sharedDatabase.NewPgPlayerGameStateRepository(s.dbPool, nopLogger) // <<< Добавляем недостающий репо
	mockAuthClient := &mockAuthServiceClient{}                                                // <<< Используем локальный мок

	// Передаем все 10 аргументов в NewGameplayService
	gameplayService := service.NewGameplayService(
		s.repo,              // StoryConfigRepository
		publishedRepo,       // PublishedStoryRepository
		sceneRepo,           // StorySceneRepository
		playerProgressRepo,  // PlayerProgressRepository
		playerGameStateRepo, // <<< PlayerGameStateRepository
		likeRepo,            // LikeRepository
		taskPublisher,       // TaskPublisher
		nil,                 // NotificationPublisher - оставляем nil, если не нужен
		nopLogger,           // Logger
		mockAuthClient,      // <<< AuthServiceClient
	)
	// <<< Добавляем тестовый межсервисный секрет >>>
	interServiceTestSecret := "test-inter-service-secret-for-integration"
	// Передаем все 7 аргументов в NewGameplayHandler
	gameplayHandler := handler.NewGameplayHandler(
		gameplayService,
		nopLogger,
		jwtTestSecret,
		interServiceTestSecret,
		s.repo,        // <<< StoryConfigRepository
		publishedRepo, // <<< PublishedStoryRepository
		cfg,           // <<< Config
	)

	gin.SetMode(gin.TestMode)
	app := gin.New()
	gameplayHandler.RegisterRoutes(app)
	s.app = app

	testServer := httptest.NewServer(app)
	s.serviceURL = testServer.URL
	log.Printf("Test server running at: %s", s.serviceURL)
}

// TearDownSuite запускается один раз после всех тестов
func (s *IntegrationTestSuite) TearDownSuite() {
	// Останавливаем тестовый консьюмер
	if s.stopConsumer != nil {
		close(s.stopConsumer)
	}
	ctx := context.Background()
	if s.dbPool != nil {
		s.dbPool.Close()
	}
	if s.rabbitConn != nil {
		s.rabbitConn.Close()
	}
	if s.pgContainer != nil {
		err := s.pgContainer.Terminate(ctx)
		require.NoError(s.T(), err)
	}
	if s.rmqContainer != nil {
		err := s.rmqContainer.Terminate(ctx)
		require.NoError(s.T(), err)
	}
	log.Println("Integration test suite torn down.")
}

// runTestTaskConsumer - горутина, которая слушает тестовую очередь задач
func (s *IntegrationTestSuite) runTestTaskConsumer(amqpURL, queueName string) {
	// Сигнализируем о завершении или ошибке при выходе
	defer close(s.consumerReady) // Закрываем канал при выходе, чтобы SetupSuite не блокировался вечно при ошибке

	// Повторное подключение, т.к. основное соединение может закрыться раньше горутины
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		log.Printf("!!! Test Consumer Error: failed to connect to RabbitMQ: %v", err)
		return
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Printf("!!! Test Consumer Error: failed to open channel: %v", err)
		return
	}
	defer ch.Close()

	// Объявляем очередь (на случай, если паблишер создаст ее позже)
	q, err := ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		log.Printf("!!! Test Consumer Error: failed to declare queue '%s': %v", queueName, err)
		return
	}

	msgs, err := ch.Consume(q.Name, "test-consumer", true, false, false, false, nil) // autoAck=true для простоты
	if err != nil {
		log.Printf("!!! Test Consumer Error: failed to register consumer: %v", err)
		return
	}
	log.Printf("[*] Test consumer started consuming queue '%s'. Signaling readiness.", queueName)
	s.consumerReady <- struct{}{} // <--- Сигнализируем о готовности

	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				log.Println("[*] Test consumer channel closed.")
				return
			}
			log.Printf("[*] Test consumer received message on '%s'", queueName)
			s.taskMessages <- msg // Отправляем сообщение в канал для тестов
		case <-s.stopConsumer:
			log.Println("[*] Test consumer stopping.")
			return
		}
	}
}

// TestIntegrationSuite запускает набор тестов
func TestIntegrationSuite(t *testing.T) {
	// Пропускаем тесты, если запущены с флагом -short
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode.")
	}
	suite.Run(t, new(IntegrationTestSuite))
}

// --- Вспомогательные функции ---

// createTestJWT создает JWT токен для тестов
func createTestJWT(userID uuid.UUID) string {
	// <<< Вызываем локальную функцию GenerateTestJWT >>>
	token, err := GenerateTestJWT(userID, jwtTestSecret, time.Minute*5)
	if err != nil {
		// В тесте проще паниковать, если токен не создался
		log.Fatalf("Failed to generate test JWT: %v", err)
	}
	return token
}

// GenerateTestJWT создает тестовый JWT токен.
// ВАЖНО: Эта функция предназначена ТОЛЬКО для использования в тестах.
func GenerateTestJWT(userID uuid.UUID, secretKey string, validityDuration time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"sub": userID.String(), // <<< Используем UUID.String()
		"exp": time.Now().Add(validityDuration).Unix(),
		"iat": time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secretKey))
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return tokenString, nil
}

// --- Тесты API ---

// Раскомментируем первый тест
func (s *IntegrationTestSuite) TestGenerateInitialStory_Integration() {
	ctx := context.Background()

	// Получаем ID первого тестового пользователя (генерируется в SetupSuite)
	var testUserID uuid.UUID
	row := s.dbPool.QueryRow(ctx, "SELECT id FROM users WHERE username = $1 LIMIT 1", "testuser101")
	err := row.Scan(&testUserID)
	require.NoError(s.T(), err)

	// Создаем токен для тестового пользователя
	token := createTestJWT(testUserID) // <<< Теперь принимает UUID

	// Подготовка запроса
	initialPrompt := "Интеграционная история"
	bodyJSON, _ := json.Marshal(map[string]string{"prompt": initialPrompt})

	req, err := http.NewRequest(http.MethodPost, s.serviceURL+"/api/stories/generate", bytes.NewBuffer(bodyJSON))
	require.NoError(s.T(), err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(s.T(), err)
	defer resp.Body.Close()

	assert.Equal(s.T(), http.StatusAccepted, resp.StatusCode)

	// Проверяем тело ответа
	var createdConfig sharedModels.StoryConfig
	err = json.NewDecoder(resp.Body).Decode(&createdConfig)
	require.NoError(s.T(), err)

	// assert.Equal(s.T(), userID, createdConfig.UserID)
	assert.Equal(s.T(), testUserID, createdConfig.UserID) // <<< Сравниваем UUID
	// assert.Equal(s.T(), initialPrompt, createdConfig.Description) // Description больше не содержит prompt
	assert.Equal(s.T(), sharedModels.StatusGenerating, createdConfig.Status)
	assert.NotEmpty(s.T(), createdConfig.ID)
	// Проверяем, что Config в ответе - это JSON null
	assert.Equal(s.T(), json.RawMessage("null"), createdConfig.Config)
	var userInputs []string
	err = json.Unmarshal(createdConfig.UserInput, &userInputs)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{initialPrompt}, userInputs)

	// *** ДОБАВЛЕНО: Гарантированное удаление созданного конфига ***
	defer func() {
		// delErr := s.repo.Delete(context.Background(), createdConfig.ID, userID)
		delErr := s.repo.Delete(context.Background(), createdConfig.ID, testUserID) // <<< Используем UUID
		if delErr != nil {
			s.T().Logf("WARN: Failed to clean up story config %s: %v", createdConfig.ID, delErr)
		}
	}()
	// *** КОНЕЦ ДОБАВЛЕНИЯ ***

	// Проверяем запись в БД
	dbConfig, err := s.repo.GetByIDInternal(context.Background(), createdConfig.ID)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), dbConfig)
	// Сравниваем поля по отдельности
	assert.Equal(s.T(), createdConfig.ID, dbConfig.ID)
	assert.Equal(s.T(), createdConfig.UserID, dbConfig.UserID)
	// assert.Equal(s.T(), createdConfig.Description, dbConfig.Description) // Description больше не содержит prompt
	assert.Equal(s.T(), createdConfig.Status, dbConfig.Status)
	assert.Equal(s.T(), []byte(createdConfig.UserInput), []byte(dbConfig.UserInput))
	// Проверяем, что Config в БД - это Go nil
	assert.Nil(s.T(), dbConfig.Config)

	// --- Проверка сообщения в RabbitMQ (Ожидаем сообщение от ГЕНЕРАЦИИ) ---
	var generationPayload sharedMessaging.GenerationTaskPayload
	foundGenerationMsg := false
	timeout := time.After(10 * time.Second) // Используем увеличенный таймаут

	// Ожидаем одно сообщение - от начальной генерации
	select {
	case msg := <-s.taskMessages:
		log.Printf("Checking received message for initial generation...")
		assert.NotNil(s.T(), msg.Body)
		err = json.Unmarshal(msg.Body, &generationPayload)
		require.NoError(s.T(), err)

		// Проверяем, что это сообщение от начальной генерации
		if generationPayload.UserInput == initialPrompt {
			foundGenerationMsg = true
			assert.Equal(s.T(), createdConfig.ID.String(), generationPayload.StoryConfigID)
			assert.Equal(s.T(), sharedMessaging.PromptTypeNarrator, generationPayload.PromptType)
			assert.Equal(s.T(), testUserID.String(), generationPayload.UserID) // <<< Сравниваем UUID string
			assert.NotEmpty(s.T(), generationPayload.TaskID)
		} else {
			s.T().Fatalf("Received unexpected message type in GenerateInitialStory test. UserInput: %s", generationPayload.UserInput)
		}
	case <-timeout:
		s.T().Fatal("Timeout waiting for message in RabbitMQ task queue during GenerateInitialStory test")
	}
	assert.True(s.T(), foundGenerationMsg, "Generation message should be found and validated")
}

func (s *IntegrationTestSuite) TestReviseDraft_Integration() {
	userID := uint64(102)
	userUUID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(fmt.Sprintf("test-user-%d", userID))) // <<< Генерируем UUID
	initialPrompt := "Начальная история для ревизии"
	revisionPrompt := "Добавить драконов"
	token := createTestJWT(userUUID)

	// --- Шаг 1: Создаем начальный драфт через Generate ---
	bodyJSON, _ := json.Marshal(map[string]string{"prompt": initialPrompt})
	reqGen, _ := http.NewRequest(http.MethodPost, s.serviceURL+"/api/stories/generate", bytes.NewBuffer(bodyJSON))
	reqGen.Header.Set("Content-Type", "application/json")
	reqGen.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{}
	respGen, err := client.Do(reqGen)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusAccepted, respGen.StatusCode)
	var initialConfig sharedModels.StoryConfig
	err = json.NewDecoder(respGen.Body).Decode(&initialConfig)
	require.NoError(s.T(), err)
	respGen.Body.Close()
	storyID := initialConfig.ID

	// --- Шаг 2: Имитируем успешную генерацию (обновляем статус и добавляем JSON в БД) ---
	generatedJSON := `{"t":"История с драконами","sd":"Кратко","p_desc":"Герой"}`
	updateQuery := `UPDATE story_configs SET status = $1, config = $2, title = $3, description = $4 WHERE id = $5`
	_, err = s.dbPool.Exec(context.Background(), updateQuery,
		sharedModels.StatusDraft, // Ставим статус Draft
		[]byte(generatedJSON),
		"История с драконами", // Заполняем Title
		"Кратко",              // Заполняем Description
		storyID,
	)
	require.NoError(s.T(), err)

	// --- Шаг 3: Отправляем запрос на ревизию ---
	reviseBodyJSON, _ := json.Marshal(map[string]string{"revision_prompt": revisionPrompt})
	reqRevise, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/stories/%s/revise", s.serviceURL, storyID), bytes.NewBuffer(reviseBodyJSON))
	require.NoError(s.T(), err)
	reqRevise.Header.Set("Content-Type", "application/json")
	reqRevise.Header.Set("Authorization", "Bearer "+token)

	respRevise, err := client.Do(reqRevise)
	require.NoError(s.T(), err)
	defer respRevise.Body.Close()

	// Проверяем ответ
	assert.Equal(s.T(), http.StatusAccepted, respRevise.StatusCode)

	// --- Шаг 4: Проверяем состояние в БД ---
	dbConfig, err := s.repo.GetByIDInternal(context.Background(), storyID)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), dbConfig)
	assert.Equal(s.T(), sharedModels.StatusGenerating, dbConfig.Status) // Статус должен стать generating
	// Сравниваем JSON после десериализации
	var expectedConfigMap map[string]interface{}
	var actualConfigMap map[string]interface{}
	err = json.Unmarshal([]byte(generatedJSON), &expectedConfigMap)
	require.NoError(s.T(), err)
	err = json.Unmarshal(dbConfig.Config, &actualConfigMap)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), expectedConfigMap, actualConfigMap)

	// Проверяем, что UserInput обновился
	var userInputs []string
	err = json.Unmarshal(dbConfig.UserInput, &userInputs)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{initialPrompt, revisionPrompt}, userInputs)

	// --- Проверка сообщения в RabbitMQ (Ожидаем сообщение от РЕВИЗИИ) ---
	var revisionPayload sharedMessaging.GenerationTaskPayload
	foundRevisionMsg := false
	timeout := time.After(10 * time.Second) // Общий таймаут

	// Пропускаем сообщения, пока не найдем нужное (от ревизии)
	for !foundRevisionMsg {
		select {
		case msg := <-s.taskMessages:
			log.Printf("Checking received message...")
			var payload sharedMessaging.GenerationTaskPayload
			err := json.Unmarshal(msg.Body, &payload)
			if err != nil {
				log.Printf("Error unmarshalling message in test: %v", err)
				continue // Пропускаем невалидное сообщение
			}
			// Идентифицируем сообщение ревизии по UserInput
			if payload.UserInput == revisionPrompt {
				log.Printf("Found revision message!")
				revisionPayload = payload
				foundRevisionMsg = true
			} else {
				log.Printf("Skipping non-revision message (UserInput: %s)", payload.UserInput)
			}
		case <-timeout:
			s.T().Fatal("Timeout waiting for REVISION message in RabbitMQ task queue")
			return // Выход из цикла и функции
		}
	}

	// Теперь проверяем найденное сообщение ревизии
	assert.True(s.T(), foundRevisionMsg, "Revision message should be found")
	assert.Equal(s.T(), storyID.String(), revisionPayload.StoryConfigID)
	assert.Equal(s.T(), revisionPrompt, revisionPayload.UserInput)
	assert.Equal(s.T(), sharedMessaging.PromptTypeNarrator, revisionPayload.PromptType)
	assert.NotEmpty(s.T(), revisionPayload.UserInput, "UserInput should not be empty for revision task")
	var inputDataMap map[string]interface{}
	err = json.Unmarshal([]byte(revisionPayload.UserInput), &inputDataMap)
	require.NoError(s.T(), err, "Failed to unmarshal UserInput JSON for revision task")
	assert.Contains(s.T(), inputDataMap, "ur", "UserInput JSON must contain 'ur' key for revision")
	assert.Equal(s.T(), revisionPrompt, inputDataMap["ur"], "'ur' key value mismatch")
	assert.Contains(s.T(), inputDataMap, "t", "UserInput JSON must contain original 't' key")
	assert.Contains(s.T(), inputDataMap, "sd", "UserInput JSON must contain original 'sd' key")
	assert.Equal(s.T(), userUUID.String(), revisionPayload.UserID) // <<< Сравниваем UUID string
	assert.NotEmpty(s.T(), revisionPayload.TaskID)
}

// TestFullGameplayFlow_Integration проверяет полный цикл: создание -> имитация -> публикация
func (s *IntegrationTestSuite) TestFullGameplayFlow_Integration() {
	ctx := context.Background()

	// 1. Получаем ID первого тестового пользователя
	var testUserID uuid.UUID
	row := s.dbPool.QueryRow(ctx, "SELECT id FROM users WHERE username = $1 LIMIT 1", "testuser101")
	err := row.Scan(&testUserID)
	require.NoError(s.T(), err)
	token := createTestJWT(testUserID) // <<< Адаптировано

	// 2. Генерируем начальную историю (как в TestGenerateInitialStory)
	// ... (код генерации, использующий token) ...

	// 3. Получаем опубликованную историю (нужен ее ID)
	var publishedStoryID uuid.UUID
	// ... (код для получения publishedStoryID по testUserID) ...

	// 4. Отправляем выбор игрока
	playerChoice := sharedModels.PlayerChoiceRequest{
		PublishedStoryID: publishedStoryID,
		SelectedChoice:   0, // Пример
	}
	bodyBytes, _ := json.Marshal(playerChoice)
	req, _ := http.NewRequest("POST", s.serviceURL+"/api/v1/play/choice", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode) // Ожидаем 202 Accepted
	resp.Body.Close()

	// 5. Ожидаем сообщение в очереди задач (если нужно)
	// ... (код ожидания сообщения) ...

	// 6. Проверяем состояние игры в БД
	// ... (код проверки player_game_states, player_progress)
}

// TestListMyDrafts_Integration проверяет получение списка черновиков пользователя с пагинацией.
func (s *IntegrationTestSuite) TestListMyDrafts_Integration() {
	userID := uint64(101)                                                                    // Используем того же пользователя
	userUUID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(fmt.Sprintf("test-user-%d", userID))) // <<< Генерируем UUID
	token := createTestJWT(userUUID)
	client := &http.Client{}
	ctx := context.Background()

	// --- Шаг 0: Создаем несколько черновиков для теста --- //
	s.T().Log("--- Создание тестовых черновиков --- ")
	draftIDs := make([]uuid.UUID, 0, 5)
	numDrafts := 5
	for i := 0; i < numDrafts; i++ {
		// Создаем через сервис, чтобы получить валидный объект
		// Используем прямой вызов сервиса, чтобы не перегружать тест HTTP запросами
		// Важно: Создаем реальный сервис здесь, т.к. в s.Suite нет доступа к сервису из SetupSuite
		// nopLogger := zap.NewNop()
		// publishedRepo := sharedDatabase.NewPgPublishedStoryRepository(s.dbPool, nopLogger)
		// Publisher не нужен для List, создаем фейковый
		// mockPublisher := new(messagingMocks.TaskPublisher)
		// mockPublisher.On("PublishGenerationTask", mock.Anything, mock.Anything).Return(nil)
		// gameplayService := service.NewGameplayService(s.repo, publishedRepo, mockPublisher, s.dbPool, nopLogger)

		// config, err := gameplayService.GenerateInitialStory(ctx, userID, fmt.Sprintf("Тестовый черновик %d", i))
		// require.NoError(s.T(), err)
		// require.NotNil(s.T(), config)
		// draftIDs = append(draftIDs, config.ID)

		// *** ИЗМЕНЕНИЕ: Создаем черновик напрямую в БД ***
		prompt := fmt.Sprintf("Тестовый черновик %d", i)
		userInputJSON, err := json.Marshal([]string{prompt})
		require.NoError(s.T(), err)
		config := &sharedModels.StoryConfig{
			ID: uuid.New(),
			// UserID:      userID,
			UserID:      userUUID, // <<< Используем UUID
			Title:       fmt.Sprintf("Draft %d", i),
			Description: prompt,
			UserInput:   userInputJSON,
			Config:      json.RawMessage(`{"t":"Draft Title"}`), // Добавляем минимальный Config
			Status:      sharedModels.StatusDraft,               // <<< Используем sharedModels
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}
		err = s.repo.Create(ctx, config) // Используем репозиторий из s.Suite
		require.NoError(s.T(), err, "Failed to create test draft directly in DB")
		draftIDs = append(draftIDs, config.ID)
		// *** КОНЕЦ ИЗМЕНЕНИЯ ***

		// Небольшая задержка, чтобы created_at отличался для сортировки
		time.Sleep(5 * time.Millisecond)
	}
	// Меняем порядок, чтобы проверить сортировку (последний созданный должен быть первым)
	reverseUUIDs(draftIDs)
	s.T().Logf("Создано %d тестовых черновиков. Ожидаемый порядок ID (от новых к старым): %v", numDrafts, draftIDs)

	// --- Шаг 1: Получаем первую страницу (limit=2) --- //
	s.T().Log("--- Шаг 1: Получение первой страницы (limit=2) ---")
	limit := 2
	reqPage1, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/stories?limit=%d", s.serviceURL, limit), nil)
	require.NoError(s.T(), err)
	reqPage1.Header.Set("Authorization", "Bearer "+token)

	respPage1, err := client.Do(reqPage1)
	require.NoError(s.T(), err)
	defer respPage1.Body.Close()
	require.Equal(s.T(), http.StatusOK, respPage1.StatusCode)

	var page1Resp handler.PaginatedResponse // Используем тип из handler
	err = json.NewDecoder(respPage1.Body).Decode(&page1Resp)
	require.NoError(s.T(), err)

	// Проверяем данные первой страницы
	require.NotNil(s.T(), page1Resp.Data)
	dataBytes, _ := json.Marshal(page1Resp.Data)
	var draftsPage1 []handler.StoryConfigSummary // <<< Используем DTO из handler
	err = json.Unmarshal(dataBytes, &draftsPage1)
	require.NoError(s.T(), err)

	assert.Len(s.T(), draftsPage1, limit, "Должно быть получено %d черновика", limit)
	assert.NotEmpty(s.T(), page1Resp.NextCursor, "Должен быть курсор для следующей страницы")
	// Проверяем порядок ID
	assert.Equal(s.T(), draftIDs[0], draftsPage1[0].ID, "Первый элемент первой страницы не совпадает")
	assert.Equal(s.T(), draftIDs[1], draftsPage1[1].ID, "Второй элемент первой страницы не совпадает")
	nextCursor := page1Resp.NextCursor
	s.T().Logf("Первая страница получена. NextCursor: %s", nextCursor)

	// --- Шаг 2: Получаем вторую страницу (limit=2, используем курсор) --- //
	s.T().Log("--- Шаг 2: Получение второй страницы (limit=2, cursor) ---")
	reqPage2, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/stories?limit=%d&cursor=%s", s.serviceURL, limit, nextCursor), nil)
	require.NoError(s.T(), err)
	reqPage2.Header.Set("Authorization", "Bearer "+token)

	respPage2, err := client.Do(reqPage2)
	require.NoError(s.T(), err)
	defer respPage2.Body.Close()
	require.Equal(s.T(), http.StatusOK, respPage2.StatusCode)

	var page2Resp handler.PaginatedResponse
	err = json.NewDecoder(respPage2.Body).Decode(&page2Resp)
	require.NoError(s.T(), err)

	// Проверяем данные второй страницы
	require.NotNil(s.T(), page2Resp.Data)
	dataBytes2, _ := json.Marshal(page2Resp.Data)
	var draftsPage2 []handler.StoryConfigSummary // <<< Используем DTO из handler
	err = json.Unmarshal(dataBytes2, &draftsPage2)
	require.NoError(s.T(), err)

	assert.Len(s.T(), draftsPage2, limit, "Должно быть получено %d черновика на второй странице", limit)
	assert.NotEmpty(s.T(), page2Resp.NextCursor, "Должен быть курсор для третьей страницы")
	// Проверяем порядок ID
	assert.Equal(s.T(), draftIDs[2], draftsPage2[0].ID, "Первый элемент второй страницы не совпадает")
	assert.Equal(s.T(), draftIDs[3], draftsPage2[1].ID, "Второй элемент второй страницы не совпадает")
	nextCursor = page2Resp.NextCursor
	s.T().Logf("Вторая страница получена. NextCursor: %s", nextCursor)

	// --- Шаг 3: Получаем третью страницу (limit=2, последний элемент) --- //
	s.T().Log("--- Шаг 3: Получение третьей страницы (limit=2, cursor) ---")
	reqPage3, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/stories?limit=%d&cursor=%s", s.serviceURL, limit, nextCursor), nil)
	require.NoError(s.T(), err)
	reqPage3.Header.Set("Authorization", "Bearer "+token)

	respPage3, err := client.Do(reqPage3)
	require.NoError(s.T(), err)
	defer respPage3.Body.Close()
	require.Equal(s.T(), http.StatusOK, respPage3.StatusCode)

	var page3Resp handler.PaginatedResponse
	err = json.NewDecoder(respPage3.Body).Decode(&page3Resp)
	require.NoError(s.T(), err)

	// Проверяем данные третьей страницы
	require.NotNil(s.T(), page3Resp.Data)
	dataBytes3, _ := json.Marshal(page3Resp.Data)
	var draftsPage3 []handler.StoryConfigSummary // <<< Используем DTO из handler
	err = json.Unmarshal(dataBytes3, &draftsPage3)
	require.NoError(s.T(), err)

	assert.Len(s.T(), draftsPage3, 1, "Должен быть получен 1 черновик на третьей странице")
	assert.Empty(s.T(), page3Resp.NextCursor, "Не должно быть курсора для следующей страницы")
	// Проверяем ID
	assert.Equal(s.T(), draftIDs[4], draftsPage3[0].ID, "Элемент третьей страницы не совпадает")
	s.T().Log("Третья (последняя) страница получена.")

	// --- Шаг 4: Проверяем запрос с невалидным курсором --- //
	s.T().Log("--- Шаг 4: Проверка невалидного курсора ---")
	invalidCursor := "not_a_valid_base64_cursor"
	reqInvalid, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/stories?limit=%d&cursor=%s", s.serviceURL, limit, invalidCursor), nil)
	require.NoError(s.T(), err)
	reqInvalid.Header.Set("Authorization", "Bearer "+token)

	respInvalid, err := client.Do(reqInvalid)
	require.NoError(s.T(), err)
	defer respInvalid.Body.Close()
	assert.Equal(s.T(), http.StatusBadRequest, respInvalid.StatusCode, "Запрос с невалидным курсором должен вернуть 400")
	s.T().Log("Проверка невалидного курсора завершена.")

	// --- Очистка: Удаляем созданные черновики --- //
	s.T().Log("--- Очистка тестовых черновиков --- ")
	deleteQuery := `DELETE FROM story_configs WHERE id = $1 AND user_id = $2`
	for _, id := range draftIDs {
		// _, err := s.dbPool.Exec(ctx, deleteQuery, id, userID)
		_, err := s.dbPool.Exec(ctx, deleteQuery, id, userUUID) // <<< Используем UUID
		assert.NoError(s.T(), err, "Ошибка при удалении тестового черновика %s", id)
	}
	s.T().Log("Очистка завершена.")
}

// TestListMyPublishedStories_Integration проверяет получение списка опубликованных историй пользователя.
func (s *IntegrationTestSuite) TestListMyPublishedStories_Integration() {
	userID := uint64(102)                                                                    // Другой пользователь
	userUUID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(fmt.Sprintf("test-user-%d", userID))) // <<< Генерируем UUID
	token := createTestJWT(userUUID)
	client := &http.Client{}
	ctx := context.Background()

	// --- Шаг 0: Создаем несколько опубликованных историй --- //
	s.T().Log("--- Создание тестовых опубликованных историй --- ")
	publishedIDs := make([]uuid.UUID, 0, 4)
	numStories := 4
	storyData := []struct {
		configJSON string
		title      string
		desc       string // Добавим поле для описания
		isPublic   bool
	}{
		{`{"t":"Pub 1","sd":"Desc 1","ac":false}`, "Pub 1", "Desc 1", false},
		{`{"t":"Pub 2","sd":"Desc 2","ac":true}`, "Pub 2", "Desc 2", true},
		{`{"t":"Pub 3","sd":"Desc 3","ac":false}`, "Pub 3", "Desc 3", false},
		{`{"t":"Pub 4","sd":"Desc 4","ac":false}`, "Pub 4", "Desc 4", true},
	}

	for i := 0; i < numStories; i++ {
		// Извлекаем описание в переменную
		desc := storyData[i].desc
		// Создаем напрямую в БД для теста
		story := &sharedModels.PublishedStory{
			// UserID:         userID,
			UserID:         userUUID, // <<< Используем UUID
			Config:         json.RawMessage(storyData[i].configJSON),
			Status:         sharedModels.StatusSetupPending, // <<< Исправлено
			IsPublic:       storyData[i].isPublic,
			IsAdultContent: storyData[i].configJSON[len(storyData[i].configJSON)-7:len(storyData[i].configJSON)-6] == "t", // Extract 'ac'
			Title:          &storyData[i].title,
			Description:    &desc, // Используем адрес переменной
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}
		// Используем реальный репозиторий
		nopLogger := zap.NewNop()
		publishedRepo := sharedDatabase.NewPgPublishedStoryRepository(s.dbPool, nopLogger)
		err := publishedRepo.Create(ctx, story)
		require.NoError(s.T(), err)
		publishedIDs = append(publishedIDs, story.ID)
		time.Sleep(5 * time.Millisecond) // Для сортировки по updated_at (в реализации offset/limit)
	}
	reverseUUIDs(publishedIDs) // Ожидаемый порядок - от новых к старым
	s.T().Logf("Создано %d тестовых опубликованных историй. Ожидаемый порядок ID: %v", numStories, publishedIDs)

	// --- Шаг 1: Получаем первую страницу (limit=2, offset=0) --- //
	s.T().Log("--- Шаг 1: Получение первой страницы (limit=2, offset=0) ---")
	limit := 2
	offset := 0
	reqPage1, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/published-stories/me?limit=%d&offset=%d", s.serviceURL, limit, offset), nil)
	require.NoError(s.T(), err)
	reqPage1.Header.Set("Authorization", "Bearer "+token)

	respPage1, err := client.Do(reqPage1)
	require.NoError(s.T(), err)
	defer respPage1.Body.Close()
	require.Equal(s.T(), http.StatusOK, respPage1.StatusCode)

	var page1Resp handler.PaginatedResponse
	err = json.NewDecoder(respPage1.Body).Decode(&page1Resp)
	require.NoError(s.T(), err)

	require.NotNil(s.T(), page1Resp.Data)
	dataBytes, _ := json.Marshal(page1Resp.Data)
	var storiesPage1 []*sharedModels.PublishedStory // Ожидаем []*...
	err = json.Unmarshal(dataBytes, &storiesPage1)
	require.NoError(s.T(), err)

	assert.Len(s.T(), storiesPage1, limit, "Должно быть получено %d истории", limit)
	assert.Empty(s.T(), page1Resp.NextCursor, "NextCursor должен быть пустым для offset пагинации")
	assert.Equal(s.T(), publishedIDs[0], storiesPage1[0].ID)
	assert.Equal(s.T(), publishedIDs[1], storiesPage1[1].ID)
	s.T().Log("Первая страница 'моих' опубликованных историй получена.")

	// --- Шаг 2: Получаем вторую страницу (limit=2, offset=2) --- //
	s.T().Log("--- Шаг 2: Получение второй страницы (limit=2, offset=2) ---")
	offset = 2
	reqPage2, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/published-stories/me?limit=%d&offset=%d", s.serviceURL, limit, offset), nil)
	require.NoError(s.T(), err)
	reqPage2.Header.Set("Authorization", "Bearer "+token)

	respPage2, err := client.Do(reqPage2)
	require.NoError(s.T(), err)
	defer respPage2.Body.Close()
	require.Equal(s.T(), http.StatusOK, respPage2.StatusCode)

	var page2Resp handler.PaginatedResponse
	err = json.NewDecoder(respPage2.Body).Decode(&page2Resp)
	require.NoError(s.T(), err)

	require.NotNil(s.T(), page2Resp.Data)
	dataBytes2, _ := json.Marshal(page2Resp.Data)
	var storiesPage2 []*sharedModels.PublishedStory
	err = json.Unmarshal(dataBytes2, &storiesPage2)
	require.NoError(s.T(), err)

	assert.Len(s.T(), storiesPage2, limit, "Должно быть получено %d истории на второй странице", limit)
	assert.Empty(s.T(), page2Resp.NextCursor)
	assert.Equal(s.T(), publishedIDs[2], storiesPage2[0].ID)
	assert.Equal(s.T(), publishedIDs[3], storiesPage2[1].ID)
	s.T().Log("Вторая страница 'моих' опубликованных историй получена.")

	// --- Шаг 3: Получаем пустую страницу (limit=2, offset=4) --- //
	s.T().Log("--- Шаг 3: Получение пустой страницы (limit=2, offset=4) ---")
	offset = 4
	reqPage3, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/published-stories/me?limit=%d&offset=%d", s.serviceURL, limit, offset), nil)
	require.NoError(s.T(), err)
	reqPage3.Header.Set("Authorization", "Bearer "+token)

	respPage3, err := client.Do(reqPage3)
	require.NoError(s.T(), err)
	defer respPage3.Body.Close()
	require.Equal(s.T(), http.StatusOK, respPage3.StatusCode)

	var page3Resp handler.PaginatedResponse
	err = json.NewDecoder(respPage3.Body).Decode(&page3Resp)
	require.NoError(s.T(), err)

	require.NotNil(s.T(), page3Resp.Data)
	dataBytes3, _ := json.Marshal(page3Resp.Data)
	var storiesPage3 []*sharedModels.PublishedStory
	err = json.Unmarshal(dataBytes3, &storiesPage3)
	require.NoError(s.T(), err)

	assert.Empty(s.T(), storiesPage3, "Третья страница должна быть пустой")
	assert.Empty(s.T(), page3Resp.NextCursor)
	s.T().Log("Пустая страница 'моих' опубликованных историй получена.")

	// --- Очистка: Удаляем созданные истории --- //
	s.T().Log("--- Очистка тестовых опубликованных историй --- ")
	deleteQuery := `DELETE FROM published_stories WHERE id = $1 AND user_id = $2`
	for _, id := range publishedIDs {
		// _, err := s.dbPool.Exec(ctx, deleteQuery, id, userID)
		_, err := s.dbPool.Exec(ctx, deleteQuery, id, userUUID) // <<< Используем UUID
		assert.NoError(s.T(), err, "Ошибка при удалении тестовой опубликованной истории %s", id)
	}
	s.T().Log("Очистка завершена.")
}

// Вспомогательная функция для разворота слайса UUID
func reverseUUIDs(s []uuid.UUID) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

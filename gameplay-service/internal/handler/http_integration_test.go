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
	"novel-server/gameplay-service/internal/models"
	"novel-server/gameplay-service/internal/repository"
	"novel-server/gameplay-service/internal/service"
	sharedMessaging "novel-server/shared/messaging"
	sharedMiddleware "novel-server/shared/middleware"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
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
	"golang.org/x/crypto/bcrypt" // Правильный импорт
)

const (
	// Путь относительно gameplay-service/internal/handler/http_integration_test.go
	migrationDir  = "../../../shared/database/migrations"
	jwtTestSecret = "test-secret-for-integration" // Тестовый JWT секрет
)

// IntegrationTestSuite определяет набор интеграционных тестов
type IntegrationTestSuite struct {
	suite.Suite
	pgContainer   *postgres.PostgresContainer
	rmqContainer  *rabbitmq.RabbitMQContainer
	dbPool        *pgxpool.Pool
	rabbitConn    *amqp.Connection
	serviceURL    string
	app           *echo.Echo
	repo          repository.StoryConfigRepository
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
	s.repo = repository.NewPostgresStoryConfigRepository(dbPool)

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
		id       uint64
		username string
		password string
	}{
		{101, "testuser101", "password101"},
		{102, "testuser102", "password102"},
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

	// --- Настройка и запуск Echo приложения для тестов ---
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

	// clientUpdatePublisher пока не нужен для этих тестов API
	// clientUpdatePublisher := messaging.NewRabbitMQClientUpdatePublisher(s.rabbitConn, cfg.ClientUpdatesQueueName)
	// require.NoError(s.T(), err)

	gameplayService := service.NewGameplayService(s.repo, taskPublisher)
	gameplayHandler := handler.NewGameplayHandler(gameplayService)

	app := echo.New()
	gameplayHandler.RegisterRoutes(app, jwtTestSecret)
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
func createTestJWT(userID uint64) string {
	// Используем функцию из shared/middleware или генерируем здесь
	// Для простоты пока возвращаем фиктивный токен, middleware должен быть настроен
	// на использование jwtTestSecret
	token, _ := sharedMiddleware.GenerateTestJWT(userID, jwtTestSecret, time.Minute*5)
	return token
}

// --- Тесты API ---

// Раскомментируем первый тест
func (s *IntegrationTestSuite) TestGenerateInitialStory_Integration() {
	userID := uint64(101)
	initialPrompt := "Интеграционная история"
	token := createTestJWT(userID)

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
	var createdConfig models.StoryConfig
	err = json.NewDecoder(resp.Body).Decode(&createdConfig)
	require.NoError(s.T(), err)

	assert.Equal(s.T(), userID, createdConfig.UserID)
	assert.Equal(s.T(), initialPrompt, createdConfig.Description)
	assert.Equal(s.T(), models.StatusGenerating, createdConfig.Status)
	assert.NotEmpty(s.T(), createdConfig.ID)
	// Проверяем, что Config в ответе - это JSON null
	assert.Equal(s.T(), json.RawMessage("null"), createdConfig.Config)
	var userInputs []string
	err = json.Unmarshal(createdConfig.UserInput, &userInputs)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{initialPrompt}, userInputs)

	// Проверяем запись в БД
	dbConfig, err := s.repo.GetByIDInternal(context.Background(), createdConfig.ID)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), dbConfig)
	// Сравниваем поля по отдельности
	assert.Equal(s.T(), createdConfig.ID, dbConfig.ID)
	assert.Equal(s.T(), createdConfig.UserID, dbConfig.UserID)
	assert.Equal(s.T(), createdConfig.Description, dbConfig.Description)
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
		if generationPayload.UserInput == initialPrompt && len(generationPayload.InputData) == 0 {
			foundGenerationMsg = true
			assert.Equal(s.T(), createdConfig.ID.String(), generationPayload.StoryConfigID)
			assert.Equal(s.T(), sharedMessaging.PromptTypeNarrator, generationPayload.PromptType)
			assert.Equal(s.T(), strconv.FormatUint(userID, 10), generationPayload.UserID)
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
	initialPrompt := "Начальная история для ревизии"
	revisionPrompt := "Добавить драконов"
	token := createTestJWT(userID)

	// --- Шаг 1: Создаем начальный драфт через Generate ---
	bodyJSON, _ := json.Marshal(map[string]string{"prompt": initialPrompt})
	reqGen, _ := http.NewRequest(http.MethodPost, s.serviceURL+"/api/stories/generate", bytes.NewBuffer(bodyJSON))
	reqGen.Header.Set("Content-Type", "application/json")
	reqGen.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{}
	respGen, err := client.Do(reqGen)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusAccepted, respGen.StatusCode)
	var initialConfig models.StoryConfig
	err = json.NewDecoder(respGen.Body).Decode(&initialConfig)
	require.NoError(s.T(), err)
	respGen.Body.Close()
	storyID := initialConfig.ID

	// --- Шаг 2: Имитируем успешную генерацию (обновляем статус и добавляем JSON в БД) ---
	generatedJSON := `{"t":"История с драконами","sd":"Кратко","p_desc":"Герой"}`
	updateQuery := `UPDATE story_configs SET status = $1, config = $2, title = $3, description = $4 WHERE id = $5`
	_, err = s.dbPool.Exec(context.Background(), updateQuery,
		models.StatusDraft, // Ставим статус Draft
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
	assert.Equal(s.T(), models.StatusGenerating, dbConfig.Status) // Статус должен стать generating
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
	assert.NotEmpty(s.T(), revisionPayload.InputData)
	assert.Contains(s.T(), revisionPayload.InputData, "current_config")
	// Сравниваем JSON после десериализации
	var expectedInputDataMap map[string]interface{}
	var actualInputDataMap map[string]interface{}
	// Десериализуем ожидаемый JSON
	err = json.Unmarshal([]byte(generatedJSON), &expectedInputDataMap)
	require.NoError(s.T(), err)
	// Получаем строку из payload и десериализуем ее
	actualInputDataStr, ok := revisionPayload.InputData["current_config"].(string)
	require.True(s.T(), ok, "InputData[\"current_config\"] should be a string")
	err = json.Unmarshal([]byte(actualInputDataStr), &actualInputDataMap)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), expectedInputDataMap, actualInputDataMap)

	assert.Equal(s.T(), strconv.FormatUint(userID, 10), revisionPayload.UserID)
	assert.NotEmpty(s.T(), revisionPayload.TaskID)
}

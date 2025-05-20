package repository

import (
	"context" // Используем стандартный пакет sql для ErrNoRows
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/internal/domain"
	"novel-server/internal/logger"

	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5" // Для проверки ошибок PostgreSQL
	"github.com/jackc/pgx/v5/pgxpool"
	// Убираем "github.com/jmoiron/sqlx"
	// Убираем "github.com/lib/pq" // PostgreSQL driver
)

// PostgresNovelRepository реализует NovelRepository для PostgreSQL.
type PostgresNovelRepository struct {
	db *pgxpool.Pool
}

// NewPostgresNovelRepository создает новый экземпляр PostgresNovelRepository.
func NewPostgresNovelRepository(db *pgxpool.Pool) *PostgresNovelRepository {
	if db == nil {
		logger.Logger.Error("database connection provided to repository is nil")
		panic("nil db")
	}
	return &PostgresNovelRepository{db: db}
}

// CreateNovel создает новую запись о новелле в хранилище.
func (r *PostgresNovelRepository) CreateNovel(ctx context.Context, userID string, config *domain.NovelConfig) (uuid.UUID, error) {
	logger.Logger.Info("CreateNovel called", "userID", userID)
	if userID == "" {
		logger.Logger.Warn("CreateNovel - empty userID")
		return uuid.Nil, fmt.Errorf("userID cannot be empty")
	}

	configData, err := json.Marshal(config)
	if err != nil {
		logger.Logger.Error("CreateNovel - error marshaling config", "err", err)
		return uuid.Nil, fmt.Errorf("failed to marshal novel config: %w", err)
	}

	novelID := uuid.New()
	query := `
		INSERT INTO novels (novel_id, user_id, title, short_description, config_data, created_at, updated_at, is_adult_content)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW(), $6)
	`

	_, err = r.db.Exec(ctx, query, novelID, userID, config.Title, config.ShortDescription, configData, config.IsAdultContent)
	if err != nil {
		logger.Logger.Error("CreateNovel - insert error", "novelID", novelID, "err", err)
		return uuid.Nil, fmt.Errorf("failed to insert novel: %w", err)
	}

	logger.Logger.Info("CreateNovel success", "novelID", novelID, "userID", userID, "isAdult", config.IsAdultContent)
	return novelID, nil
}

// GetNovelMetadataByID возвращает краткую информацию (метаданные) о новелле по ID.
func (r *PostgresNovelRepository) GetNovelMetadataByID(ctx context.Context, novelID uuid.UUID, userID string) (*domain.NovelMetadata, error) {
	logger.Logger.Info("GetNovelMetadataByID", "novelID", novelID, "userID", userID)
	query := `SELECT novel_id, user_id, title, COALESCE(short_description, '') as short_description, created_at, updated_at
			  FROM novels
			  WHERE novel_id = $1 AND user_id = $2`

	var meta domain.NovelMetadata
	row := r.db.QueryRow(ctx, query, novelID, userID)
	err := row.Scan(&meta.NovelID, &meta.UserID, &meta.Title, &meta.ShortDescription, &meta.CreatedAt, &meta.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			logger.Logger.Warn("GetNovelMetadataByID - not found or access denied", "novelID", novelID, "userID", userID)
			return nil, fmt.Errorf("novel not found or not owned by user")
		}
		logger.Logger.Error("GetNovelMetadataByID - query error", "err", err)
		return nil, fmt.Errorf("failed to get novel metadata: %w", err)
	}
	logger.Logger.Info("GetNovelMetadataByID - found", "title", meta.Title)
	return &meta, nil
}

// GetNovelConfigByID возвращает полную конфигурацию новеллы по ID.
func (r *PostgresNovelRepository) GetNovelConfigByID(ctx context.Context, novelID uuid.UUID, userID string) (*domain.NovelConfig, error) {
	log.Printf("[Repo] GetNovelConfigByID called for NovelID: %s, UserID: %s", novelID, userID)
	query := `SELECT title, COALESCE(short_description, '') as short_description, config_data FROM novels WHERE novel_id = $1`

	var title, shortDescription string
	var configJSON []byte
	row := r.db.QueryRow(ctx, query, novelID)
	err := row.Scan(&title, &shortDescription, &configJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] GetNovelConfigByID - Novel not found: %s", novelID)
			return nil, fmt.Errorf("novel not found")
		}
		log.Printf("[Repo] GetNovelConfigByID - Error scanning config: %v", err)
		return nil, fmt.Errorf("failed to get novel config: %w", err)
	}

	var config domain.NovelConfig
	if err := json.Unmarshal(configJSON, &config); err != nil {
		log.Printf("[Repo] GetNovelConfigByID - Error unmarshaling config: %v", err)
		return nil, fmt.Errorf("failed to unmarshal novel config data: %w", err)
	}

	// Добавляем поля title и shortDescription из основной таблицы
	config.Title = title
	config.ShortDescription = shortDescription

	// Загружаем сетап для получения персонажей и фонов (опционально)
	setupStateData, err := r.GetNovelSetupState(ctx, novelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] GetNovelConfigByID - Setup not found for novel %s, returning config without setup data", novelID)
			// Сетап не найден, возвращаем только конфиг
		} else {
			// Другая ошибка при загрузке сетапа
			logger.Logger.Error("GetNovelConfigByID - setup state error", "novelID", novelID, "err", err)
			// Можно вернуть ошибку или только конфиг, зависит от требований
		}
	} else {
		// Сетап загружен, логируем размер
		logger.Logger.Info("GetNovelConfigByID - loaded setup state", "length", len(setupStateData), "novelID", novelID)
	}

	// TODO: Решить, как интегрировать данные сетапа в ответ (если нужно)

	logger.Logger.Info("GetNovelConfigByID success", "novelID", novelID)
	return &config, nil
}

// ListNovelsByUser возвращает список метаданных новелл для указанного пользователя.
func (r *PostgresNovelRepository) ListNovelsByUser(ctx context.Context, userID string, limit, offset int) ([]domain.NovelMetadata, error) {
	logger.Logger.Info("ListNovelsByUser called", "userID", userID, "limit", limit, "offset", offset)
	query := `SELECT novel_id, user_id, title, COALESCE(short_description, '') as short_description, created_at, updated_at
			  FROM novels
			  WHERE user_id = $1
			  ORDER BY updated_at DESC
			  LIMIT $2 OFFSET $3`

	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		logger.Logger.Error("ListNovelsByUser - query error", "err", err)
		return nil, fmt.Errorf("failed to list novels: %w", err)
	}
	defer rows.Close()

	novels := []domain.NovelMetadata{}
	for rows.Next() {
		var meta domain.NovelMetadata
		if err := rows.Scan(&meta.NovelID, &meta.UserID, &meta.Title, &meta.ShortDescription, &meta.CreatedAt, &meta.UpdatedAt); err != nil {
			logger.Logger.Error("ListNovelsByUser - scan error", "err", err)
			return nil, fmt.Errorf("failed to process novel list: %w", err)
		}
		novels = append(novels, meta)
	}

	if err := rows.Err(); err != nil {
		logger.Logger.Error("ListNovelsByUser - rows error", "err", err)
		return nil, fmt.Errorf("error reading novel list: %w", err)
	}

	logger.Logger.Info("ListNovelsByUser success", "count", len(novels), "userID", userID)
	return novels, nil
}

// SaveNovelState сохраняет состояние новеллы (stateData) для определенной сцены
// Если в stateData значение current_stage равно "setup", то данные сохраняются в таблицу novels в поле setup_state_data.
func (r *PostgresNovelRepository) SaveNovelState(ctx context.Context, novelID uuid.UUID, sceneIndex int, userID string, stateHash string, stateData []byte) error {
	log.Printf("[Repo] Saving state. NovelID: %s, SceneIndex: %d, UserID: %s, Hash: %s", novelID, sceneIndex, userID, stateHash)

	// Проверяем, является ли состояние сетапом по значению current_stage
	var state struct {
		CurrentStage string `json:"current_stage"`
	}
	if err := json.Unmarshal(stateData, &state); err != nil {
		log.Printf("[Repo] Warning: Failed to unmarshal state to check current_stage: %v", err)
		// Продолжаем выполнение даже при ошибке десериализации
	}

	// Определяем, является ли это сетапом по значению current_stage
	isSetup := state.CurrentStage == "setup"

	// Если это сетап, сохраняем ТОЛЬКО в таблицу novels
	if isSetup {
		log.Printf("[Repo] Detected setup state (current_stage='setup'). Saving ONLY to novels table. NovelID: %s", novelID)
		// Сохраняем сетап в поле setup_state_data таблицы novels
		err := r.SaveNovelSetupState(ctx, novelID, stateData)
		if err != nil {
			return fmt.Errorf("failed to save setup state: %w", err)
		}

		// Обновляем прогресс пользователя для сетапа
		updateUserProgressQuery := `
			INSERT INTO user_novel_progress (novel_id, user_id, current_scene_index, updated_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (novel_id, user_id) DO UPDATE
			SET current_scene_index = CASE
				WHEN $3 > user_novel_progress.current_scene_index THEN $3
				ELSE user_novel_progress.current_scene_index
			END,
			updated_at = NOW();
		`

		_, err = r.db.Exec(ctx, updateUserProgressQuery, novelID, userID, sceneIndex)
		if err != nil {
			log.Printf("[Repo] Warning: Could not update user progress for setup: %v", err)
			// Не возвращаем ошибку, так как основное состояние уже сохранено
		}

		log.Printf("[Repo] Setup state saved only to novels table. Not saving to novel_states. NovelID: %s", novelID)
		return nil
	}

	// Для обычных сцен (не сетап) сохраняем в novel_states
	query := `
		INSERT INTO novel_states (novel_id, scene_index, state_hash, state_data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (novel_id, scene_index, state_hash) DO UPDATE
		SET updated_at = NOW();
	`

	// Сохраняем в БД, обновляя updated_at в случае конфликта ключей
	_, err := r.db.Exec(ctx, query, novelID, sceneIndex, stateHash, stateData)
	if err != nil {
		log.Printf("[Repo] Error saving state: %v", err)
		return fmt.Errorf("failed to save novel state: %w", err)
	}

	// Обновляем прогресс пользователя в таблице user_novel_progress
	updateUserProgressQuery := `
		INSERT INTO user_novel_progress (novel_id, user_id, current_scene_index, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (novel_id, user_id) DO UPDATE
		SET current_scene_index = CASE
			WHEN $3 > user_novel_progress.current_scene_index THEN $3
			ELSE user_novel_progress.current_scene_index
		END,
		updated_at = NOW();
	`

	_, err = r.db.Exec(ctx, updateUserProgressQuery, novelID, userID, sceneIndex)
	if err != nil {
		log.Printf("[Repo] Warning: Could not update user progress: %v", err)
		// Не возвращаем ошибку, так как основное состояние уже сохранено
	}

	return nil
}

// GetLatestNovelState возвращает самое последнее сохраненное состояние новеллы (stateData)
// и его индекс сцены для конкретного пользователя.
func (r *PostgresNovelRepository) GetLatestNovelState(ctx context.Context, novelID uuid.UUID, userID string) (stateData []byte, sceneIndex int, err error) {
	log.Printf("[Repo] Getting latest state. NovelID: %s, UserID: %s", novelID, userID)

	// Сначала проверяем прогресс пользователя в таблице user_novel_progress
	var currentSceneIndex int
	progressQuery := `
		SELECT current_scene_index 
		FROM user_novel_progress 
		WHERE novel_id = $1 AND user_id = $2
		LIMIT 1;
	`

	err = r.db.QueryRow(ctx, progressQuery, novelID, userID).Scan(&currentSceneIndex)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Если нет записи в таблице прогресса, это новый пользователь
			log.Printf("[Repo] No progress record found for user. NovelID: %s, UserID: %s. Checking for existing scenes.", novelID, userID)

			// Проверяем, существуют ли уже сгенерированные сцены для этой новеллы
			// Сначала проверяем сцену с индексом 0
			existingSceneQuery := `
				SELECT state_data 
				FROM novel_states 
				WHERE novel_id = $1 AND scene_index = 0
				ORDER BY created_at ASC 
				LIMIT 1;
			`

			var existingStateData []byte
			err = r.db.QueryRow(ctx, existingSceneQuery, novelID).Scan(&existingStateData)
			if err == nil {
				// Нашли существующую сцену с индексом 0, возвращаем её
				log.Printf("[Repo] Found existing scene 0 for NovelID: %s. Using this for new user %s", novelID, userID)
				return existingStateData, 0, nil
			}

			// Если сцены с индексом 0 нет, проверяем старую логику для обратной совместимости
			log.Printf("[Repo] No existing scene 0 found. Checking old schema for user-specific state.")

			fallbackQuery := `
				SELECT scene_index, state_data 
				FROM novel_states 
				WHERE novel_id = $1 AND user_id = $2
				ORDER BY scene_index DESC, updated_at DESC 
				LIMIT 1;
			`

			err = r.db.QueryRow(ctx, fallbackQuery, novelID, userID).Scan(&sceneIndex, &stateData)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					// Если прямой запрос не нашел данных, проверяем наличие сетапа (сцены с current_stage="setup")
					log.Printf("[Repo] No direct state found, checking for setup state. NovelID: %s, UserID: %s", novelID, userID)

					setupData, setupErr := r.GetNovelSetupState(ctx, novelID)
					if setupErr == nil {
						// Нашли сетап, определяем его scene_index
						var setupState struct {
							CurrentStage string `json:"current_stage"`
							Scenes       []struct {
								Index int `json:"index"`
							} `json:"scenes"`
						}

						setupSceneIndex := 0 // По умолчанию считаем, что сетап имеет индекс 0

						if err := json.Unmarshal(setupData, &setupState); err == nil {
							// Если смогли распарсить JSON и найти индекс сцены, используем его
							if len(setupState.Scenes) > 0 {
								setupSceneIndex = setupState.Scenes[0].Index
							}
						}

						log.Printf("[Repo] Returning setup state (current_stage='setup'). NovelID: %s, UserID: %s, SceneIndex: %d",
							novelID, userID, setupSceneIndex)
						return setupData, setupSceneIndex, nil
					}

					log.Printf("[Repo] No latest state found for user. NovelID: %s, UserID: %s", novelID, userID)
					// Возвращаем -1 как индикатор отсутствия состояния, а не ошибку
					return nil, -1, nil
				}
				log.Printf("[Repo] Error getting latest state directly: %v", err)
				return nil, -1, fmt.Errorf("failed to get latest novel state: %w", err)
			}

			log.Printf("[Repo] Found latest state directly. NovelID: %s, UserID: %s, SceneIndex: %d", novelID, userID, sceneIndex)
			return stateData, sceneIndex, nil
		}

		log.Printf("[Repo] Error getting user progress: %v", err)
		return nil, -1, fmt.Errorf("failed to get user progress: %w", err)
	}

	// Проверяем, нужно ли использовать сетап для этой сцены
	// Сначала проверяем наличие сетапа в таблице novels
	setupData, setupErr := r.GetNovelSetupState(ctx, novelID)
	if setupErr == nil {
		// Нашли сетап, определяем его scene_index
		var setupState struct {
			CurrentStage string `json:"current_stage"`
			Scenes       []struct {
				Index int `json:"index"`
			} `json:"scenes"`
		}

		if err := json.Unmarshal(setupData, &setupState); err == nil {
			// Проверяем, совпадает ли индекс сцены с текущим прогрессом
			if len(setupState.Scenes) > 0 && setupState.Scenes[0].Index == currentSceneIndex {
				// Если это сетап и его индекс совпадает с текущим прогрессом, возвращаем его
				log.Printf("[Repo] Using setup state for current progress. NovelID: %s, UserID: %s, SceneIndex: %d",
					novelID, userID, currentSceneIndex)
				return setupData, currentSceneIndex, nil
			}
		}
	}

	// Если нашли прогресс, получаем состояние по сцене, без учета user_id
	// Берем самое раннее состояние для данной сцены, которое должно быть общим для всех пользователей
	stateQuery := `
		SELECT state_data 
		FROM novel_states 
		WHERE novel_id = $1 AND scene_index = $2
		ORDER BY created_at ASC
		LIMIT 1;
	`

	err = r.db.QueryRow(ctx, stateQuery, novelID, currentSceneIndex).Scan(&stateData)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] No state found for scene %d. NovelID: %s", currentSceneIndex, novelID)
			return nil, -1, nil
		}
		log.Printf("[Repo] Error getting state for scene %d: %v", currentSceneIndex, err)
		return nil, -1, fmt.Errorf("failed to get novel state for scene %d: %w", currentSceneIndex, err)
	}

	log.Printf("[Repo] Found latest state via progress. NovelID: %s, UserID: %s, SceneIndex: %d", novelID, userID, currentSceneIndex)
	return stateData, currentSceneIndex, nil
}

// GetNovelStateByHash возвращает состояние новеллы (stateData) по его хешу.
// Возвращает ошибку ErrNoRows, если состояние с таким хешом не найдено.
func (r *PostgresNovelRepository) GetNovelStateByHash(ctx context.Context, stateHash string) (stateData []byte, err error) {
	query := `SELECT state_data FROM novel_states WHERE state_hash = $1 LIMIT 1;`
	log.Printf("[Repo] Getting state by hash. Hash: %s", stateHash)
	err = r.db.QueryRow(ctx, query, stateHash).Scan(&stateData)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] State not found by hash: %s", stateHash)
			return nil, pgx.ErrNoRows // Возвращаем стандартную ошибку
		}
		log.Printf("[Repo] Error getting state by hash: %v", err)
		return nil, fmt.Errorf("failed to get novel state by hash: %w", err)
	}
	log.Printf("[Repo] Found state by hash: %s", stateHash)
	return stateData, nil
}

// GetNovelStateBySceneIndex возвращает состояние новеллы (stateData) для определенного индекса сцены.
// Возвращает самое раннее состояние (первое созданное), которое должно быть общим для всех пользователей.
// Возвращает ошибку ErrNoRows, если состояние с таким индексом сцены не найдено.
func (r *PostgresNovelRepository) GetNovelStateBySceneIndex(ctx context.Context, novelID uuid.UUID, sceneIndex int) (stateData []byte, err error) {
	query := `
		SELECT state_data 
		FROM novel_states 
		WHERE novel_id = $1 AND scene_index = $2
		ORDER BY created_at ASC
		LIMIT 1;
	`

	log.Printf("[Repo] Getting state by scene index. NovelID: %s, SceneIndex: %d", novelID, sceneIndex)

	err = r.db.QueryRow(ctx, query, novelID, sceneIndex).Scan(&stateData)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] State not found for scene index: %d in novel: %s", sceneIndex, novelID)
			return nil, pgx.ErrNoRows
		}
		log.Printf("[Repo] Error getting state by scene index: %v", err)
		return nil, fmt.Errorf("failed to get novel state by scene index: %w", err)
	}

	log.Printf("[Repo] Found state for scene index: %d in novel: %s", sceneIndex, novelID)
	return stateData, nil
}

// GetNovelSetupState возвращает сетап новеллы (состояние со значением current_stage="setup").
// Сначала проверяем наличие сетапа в таблице novels, если там нет - ищем в таблице novel_states
func (r *PostgresNovelRepository) GetNovelSetupState(ctx context.Context, novelID uuid.UUID) (stateData []byte, err error) {
	// Сначала пытаемся получить сетап из таблицы novels (новый способ)
	query := `SELECT setup_state_data FROM novels WHERE novel_id = $1 AND setup_state_data IS NOT NULL;`

	log.Printf("[Repo] Getting setup state from novels table. NovelID: %s", novelID)
	err = r.db.QueryRow(ctx, query, novelID).Scan(&stateData)
	if err == nil {
		log.Printf("[Repo] Found setup state in novels table. NovelID: %s", novelID)
		return stateData, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[Repo] Error getting setup state from novels table: %v", err)
		return nil, fmt.Errorf("failed to get novel setup state from novels table: %w", err)
	}

	// Если не нашли в novels, ищем в novel_states состояния с current_stage = "setup"
	log.Printf("[Repo] Setup state not found in novels table. Trying novel_states. NovelID: %s", novelID)

	// Получаем состояния и проверяем их поле current_stage
	queryOld := `
		SELECT state_data, user_id, created_at, state_hash  
		FROM novel_states 
		WHERE novel_id = $1
		ORDER BY created_at ASC;
	`

	rows, err := r.db.Query(ctx, queryOld, novelID)
	if err != nil {
		log.Printf("[Repo] Error querying novel_states: %v", err)
		return nil, fmt.Errorf("failed to query novel states: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tempStateData []byte
		var userID string
		var createdAt time.Time
		var stateHash string

		err := rows.Scan(&tempStateData, &userID, &createdAt, &stateHash)
		if err != nil {
			log.Printf("[Repo] Error scanning row: %v", err)
			continue
		}

		// Проверяем, является ли состояние сетапом по полю current_stage
		var state struct {
			CurrentStage string `json:"current_stage"`
		}
		if err := json.Unmarshal(tempStateData, &state); err != nil {
			log.Printf("[Repo] Error unmarshaling state data: %v", err)
			continue
		}

		if state.CurrentStage == "setup" {
			log.Printf("[Repo] Found setup state (current_stage='setup') in novel_states. NovelID: %s, UserID: %s, CreatedAt: %s, StateHash: %s",
				novelID, userID, createdAt.Format(time.RFC3339), stateHash)

			// Сохраняем найденный сетап в таблицу novels для будущего использования
			err = r.SaveNovelSetupState(ctx, novelID, tempStateData)
			if err != nil {
				log.Printf("[Repo] Warning: Failed to save setup state to novels table: %v", err)
				// Не возвращаем ошибку, так как сетап мы все равно нашли
			}

			return tempStateData, nil
		}
	}

	if err := rows.Err(); err != nil {
		log.Printf("[Repo] Error iterating rows: %v", err)
		return nil, fmt.Errorf("error reading novel states: %w", err)
	}

	// Если сетап не найден ни в одной из таблиц
	log.Printf("[Repo] Setup state not found for NovelID: %s", novelID)
	return nil, pgx.ErrNoRows
}

// SaveNovelSetupState сохраняет сетап новеллы в поле setup_state_data таблицы novels
func (r *PostgresNovelRepository) SaveNovelSetupState(ctx context.Context, novelID uuid.UUID, setupData []byte) error {
	query := `UPDATE novels SET setup_state_data = $1, updated_at = CURRENT_TIMESTAMP WHERE novel_id = $2;`

	log.Printf("[Repo] Saving setup state to novels table. NovelID: %s", novelID)
	_, err := r.db.Exec(ctx, query, setupData, novelID)
	if err != nil {
		log.Printf("[Repo] Error saving setup state to novels table: %v", err)
		return fmt.Errorf("failed to save setup state to novels table: %w", err)
	}

	log.Printf("[Repo] Successfully saved setup state to novels table. NovelID: %s", novelID)
	return nil
}

// ListNovels возвращает список новелл с поддержкой курсорной пагинации и информацией о прогрессе пользователя.
func (r *PostgresNovelRepository) ListNovels(ctx context.Context, userID string, limit int, cursor *uuid.UUID) ([]domain.NovelListItem, int, *uuid.UUID, error) {
	log.Printf("[Repo] ListNovels called for UserID: %s, Limit: %d, Cursor: %v", userID, limit, cursor)

	if userID == "" {
		log.Println("[Repo] ListNovels - Error: userID is required to get progress.")
		return nil, 0, nil, fmt.Errorf("userID is required to list novels with progress")
	}

	if limit <= 0 {
		limit = 10
	}

	// Используем strings.Builder для построения запроса и слайс для аргументов
	var queryBuilder strings.Builder
	args := []interface{}{}
	paramCount := 0

	queryBuilder.WriteString(`
		SELECT
			n.novel_id,
			n.title,
			COALESCE(n.short_description, '') as short_description,
			n.config_data,
			n.created_at,
			n.updated_at,
			n.is_adult_content,
			(n.setup_state_data IS NOT NULL OR EXISTS(SELECT 1 FROM novel_states ns_setup WHERE ns_setup.novel_id = n.novel_id AND ns_setup.scene_index = 0)) as is_setuped,
			(
				SELECT up.current_scene_index
				FROM user_novel_progress up
				WHERE up.novel_id = n.novel_id AND up.user_id = $1
				LIMIT 1
			) as current_user_scene_index
		FROM novels n
	`)
	args = append(args, userID)
	paramCount++ // $1 = userID

	// Добавляем условие для курсорной пагинации, если курсор предоставлен
	if cursor != nil {
		// Получаем created_at для курсора (отдельным запросом для простоты)
		var cursorCreatedAt time.Time
		cursorQuery := `SELECT created_at FROM novels WHERE novel_id = $1`
		if err := r.db.QueryRow(ctx, cursorQuery, *cursor).Scan(&cursorCreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				log.Printf("[Repo] ListNovels - Cursor novel not found: %s", *cursor)
				return nil, 0, nil, fmt.Errorf("cursor novel not found")
			}
			log.Printf("[Repo] ListNovels - Error fetching cursor created_at: %v", err)
			return nil, 0, nil, fmt.Errorf("failed to fetch cursor data: %w", err)
		}

		// Добавляем условие WHERE (Keyset pagination)
		// (created_at < cursor_created_at) OR (created_at = cursor_created_at AND novel_id < cursor_novel_id)
		queryBuilder.WriteString(fmt.Sprintf(`
			WHERE (n.created_at < $%d) OR (n.created_at = $%d AND n.novel_id < $%d)
		`, paramCount+1, paramCount+2, paramCount+3))
		args = append(args, cursorCreatedAt, cursorCreatedAt, *cursor)
		paramCount += 3
	}

	// Добавляем сортировку и лимит
	queryBuilder.WriteString(fmt.Sprintf(`
		ORDER BY n.created_at DESC, n.novel_id DESC
		LIMIT $%d
	`, paramCount+1))
	args = append(args, limit+1) // Запрашиваем на 1 больше для определения hasMore
	paramCount++

	query := queryBuilder.String()
	log.Printf("[Repo] ListNovels - Executing query: %s with args: %v", query, args)

	// Выполняем запрос
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		log.Printf("[Repo] ListNovels - Error querying novels: %v", err)
		return nil, 0, nil, fmt.Errorf("failed to list novels: %w", err)
	}
	defer rows.Close()

	// Обработка результатов
	novels := []domain.NovelListItem{}
	for rows.Next() {
		var item domain.NovelListItem
		var configData []byte
		var shortDescription string
		var isSetuped bool
		var currentUserSceneIndex sql.NullInt64
		var isAdultContent bool

		if err := rows.Scan(
			&item.NovelID,
			&item.Title,
			&shortDescription,
			&configData,
			&item.CreatedAt,
			&item.UpdatedAt,
			&isAdultContent,
			&isSetuped,
			&currentUserSceneIndex,
		); err != nil {
			log.Printf("[Repo] ListNovels - Error scanning row: %v", err)
			return nil, 0, nil, fmt.Errorf("failed to process novel list: %w", err)
		}

		item.IsAdultContent = isAdultContent
		item.IsSetuped = isSetuped

		if currentUserSceneIndex.Valid {
			item.IsStartedByUser = true
			sceneIndex := int(currentUserSceneIndex.Int64)
			item.CurrentUserSceneIndex = &sceneIndex
		} else {
			item.IsStartedByUser = false
			item.CurrentUserSceneIndex = nil
		}

		item.ShortDescription = shortDescription

		var config domain.NovelConfig
		if err := json.Unmarshal(configData, &config); err != nil {
			log.Printf("[Repo] ListNovels - Error unmarshaling config for NovelID %s: %v", item.NovelID, err)
			item.TotalScenesCount = 0
			if item.ShortDescription == "" {
				item.ShortDescription = "Описание недоступно"
			}
		} else {
			item.TotalScenesCount = determineSceneCountFromLength(config.StoryConfig.Length)
			if item.ShortDescription == "" && config.ShortDescription != "" {
				item.ShortDescription = config.ShortDescription
			}
		}

		novels = append(novels, item)
	}

	if err := rows.Err(); err != nil {
		log.Printf("[Repo] ListNovels - Error after iterating rows: %v", err)
		return nil, 0, nil, fmt.Errorf("error reading novel list: %w", err)
	}

	// Получаем общее количество новелл (только засетапленные)
	var totalCount int
	countQuery := `SELECT COUNT(*) FROM novels WHERE setup_state_data IS NOT NULL OR EXISTS (SELECT 1 FROM novel_states ns WHERE ns.novel_id = novels.novel_id AND ns.scene_index = 0)`
	if err := r.db.QueryRow(ctx, countQuery).Scan(&totalCount); err != nil {
		log.Printf("[Repo] ListNovels - Error counting total setuped novels: %v", err)
		totalCount = 0 // Не критично, если счетчик не сработает
	}

	// Определяем hasMore и nextCursor
	hasMore := false
	var nextCursor *uuid.UUID
	if len(novels) > limit {
		hasMore = true
		nextCursor = &novels[limit-1].NovelID // Курсор на последний элемент *страницы*
		novels = novels[:limit]               // Обрезаем лишний элемент
	}

	log.Printf("[Repo] ListNovels - Found %d novels for UserID %s (hasMore: %v)", len(novels), userID, hasMore)
	return novels, totalCount, nextCursor, nil
}

// Вспомогательная функция для определения количества сцен по строке длины
// (Эта функция должна быть идентична той, что используется в NovelContentService)
func determineSceneCountFromLength(length string) int {
	// TODO: Скопировать или вынести логику из NovelContentService.determineSceneCount
	// Примерная реализация:
	switch length {
	case "short":
		return 5 // Пример
	case "medium":
		return 10 // Пример
	case "long":
		return 15 // Пример
	default:
		return 10 // Значение по умолчанию
	}
}

// GetNovelDetails возвращает детальную информацию о новелле, включая персонажей из сетапа
func (r *PostgresNovelRepository) GetNovelDetails(ctx context.Context, novelID uuid.UUID) (*domain.NovelDetailsResponse, error) {
	log.Printf("[Repo] GetNovelDetails called for NovelID: %s", novelID)

	// Получаем основную информацию о новелле
	query := `
		SELECT n.novel_id, n.title, COALESCE(n.short_description, '') as short_description, n.config_data, n.created_at, n.updated_at,
			   (SELECT COUNT(*) FROM novel_states ns WHERE ns.novel_id = n.novel_id) as scenes_count,
			   (n.setup_state_data IS NOT NULL OR EXISTS(SELECT 1 FROM novel_states ns WHERE ns.novel_id = n.novel_id AND ns.scene_index = 0)) as is_setuped
		FROM novels n
		WHERE n.novel_id = $1
	`

	var novelDetails domain.NovelDetailsResponse
	var configJSON []byte
	var isSetuped bool
	var shortDescription string

	// Выполняем запрос
	err := r.db.QueryRow(ctx, query, novelID).Scan(
		&novelDetails.NovelID,
		&novelDetails.Title,
		&shortDescription,
		&configJSON,
		&novelDetails.CreatedAt,
		&novelDetails.UpdatedAt,
		&novelDetails.ScenesCount,
		&isSetuped,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] GetNovelDetails - Novel not found: %s", novelID)
			return nil, fmt.Errorf("novel not found")
		}
		log.Printf("[Repo] GetNovelDetails - Error querying novel: %v", err)
		return nil, fmt.Errorf("failed to get novel details: %w", err)
	}

	novelDetails.ShortDescription = shortDescription

	// Распаковываем конфигурацию
	var config domain.NovelConfig
	if err := json.Unmarshal(configJSON, &config); err != nil {
		log.Printf("[Repo] GetNovelDetails - Error unmarshaling config: %v", err)
		// Не возвращаем ошибку, просто оставляем пустые поля конфигурации
	} else {
		novelDetails.Genre = config.Genre
		novelDetails.Language = config.Language
		novelDetails.WorldContext = config.WorldContext
		novelDetails.EndingPreference = config.EndingPreference
		novelDetails.PlayerName = config.PlayerName
		novelDetails.PlayerGender = config.PlayerGender
		// Если описание новеллы пустое в таблице novels, берем из config
		if novelDetails.ShortDescription == "" && config.ShortDescription != "" {
			novelDetails.ShortDescription = config.ShortDescription
		}
	}

	// Если новелла не настроена (нет setup сцены), возвращаем ошибку
	if !isSetuped {
		log.Printf("[Repo] GetNovelDetails - Novel not setuped: %s", novelID)
		return nil, fmt.Errorf("novel not setuped")
	}

	// Пытаемся получить персонажей из настройки
	setupStateData, err := r.GetNovelSetupState(ctx, novelID)
	if err != nil {
		log.Printf("[Repo] GetNovelDetails - Error getting setup state: %v", err)
		// Не возвращаем ошибку, просто оставляем пустой список персонажей
	} else {
		// Распаковываем настройку для получения персонажей
		var setupState domain.NovelState
		if err := json.Unmarshal(setupStateData, &setupState); err != nil {
			log.Printf("[Repo] GetNovelDetails - Error unmarshaling setup state: %v", err)
			// Не возвращаем ошибку, оставляем пустой список персонажей
		} else {
			// Извлекаем персонажей из состояния
			novelDetails.Characters = setupState.Characters
		}
	}

	log.Printf("[Repo] GetNovelDetails - Successfully retrieved details for NovelID: %s", novelID)
	return &novelDetails, nil
}

// GetNovelIsAdult возвращает флаг is_adult_content для новеллы.
func (r *PostgresNovelRepository) GetNovelIsAdult(ctx context.Context, novelID uuid.UUID) (bool, error) {
	log.Printf("[Repo] GetNovelIsAdult called for NovelID: %s", novelID)
	var isAdult bool
	query := `SELECT is_adult_content FROM novels WHERE novel_id = $1`
	err := r.db.QueryRow(ctx, query, novelID).Scan(&isAdult)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] GetNovelIsAdult - Novel not found: %s", novelID)
			return false, fmt.Errorf("novel not found")
		}
		log.Printf("[Repo] GetNovelIsAdult - Error querying is_adult_content: %v", err)
		return false, fmt.Errorf("failed to get is_adult_content flag: %w", err)
	}
	log.Printf("[Repo] GetNovelIsAdult - NovelID: %s, IsAdult: %t", novelID, isAdult)
	return isAdult, nil
}

// DB возвращает пул соединений с базой данных для низкоуровневых операций
func (r *PostgresNovelRepository) DB() *pgxpool.Pool {
	return r.db
}

// GetUserNovelProgress возвращает текущий прогресс пользователя в новелле
// (индекс последней доступной сцены).
// Возвращает -1, если прогресс не найден.
func (r *PostgresNovelRepository) GetUserNovelProgress(ctx context.Context, novelID uuid.UUID, userID string) (sceneIndex int, err error) {
	log.Printf("[Repo] Getting user progress. NovelID: %s, UserID: %s", novelID, userID)

	query := `
		SELECT current_scene_index
		FROM user_novel_progress
		WHERE novel_id = $1 AND user_id = $2
		LIMIT 1;
	`

	err = r.db.QueryRow(ctx, query, novelID, userID).Scan(&sceneIndex)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] No progress found for user. NovelID: %s, UserID: %s", novelID, userID)

			// Проверяем старую схему для обратной совместимости
			fallbackQuery := `
				SELECT MAX(scene_index)
				FROM novel_states
				WHERE novel_id = $1 AND user_id = $2;
			`

			var maxSceneIndex sql.NullInt64
			err = r.db.QueryRow(ctx, fallbackQuery, novelID, userID).Scan(&maxSceneIndex)
			if err != nil {
				log.Printf("[Repo] Error getting fallback progress: %v", err)
				return -1, fmt.Errorf("failed to get fallback user progress: %w", err)
			}

			if maxSceneIndex.Valid {
				sceneIndex = int(maxSceneIndex.Int64)
				log.Printf("[Repo] Found fallback progress. NovelID: %s, UserID: %s, SceneIndex: %d", novelID, userID, sceneIndex)
				return sceneIndex, nil
			}

			return -1, nil
		}

		log.Printf("[Repo] Error getting user progress: %v", err)
		return -1, fmt.Errorf("failed to get user progress: %w", err)
	}

	log.Printf("[Repo] Found user progress. NovelID: %s, UserID: %s, SceneIndex: %d", novelID, userID, sceneIndex)
	return sceneIndex, nil
}

// SaveUserStoryProgress сохраняет динамические элементы прогресса пользователя
// для конкретной сцены новеллы.
func (r *PostgresNovelRepository) SaveUserStoryProgress(ctx context.Context, novelID uuid.UUID, sceneIndex int, userID string,
	progress *domain.UserStoryProgress) error {

	// Проверяем входные данные
	if progress == nil {
		return fmt.Errorf("progress is nil")
	}

	// Если хеш не указан, это ошибка - нельзя сохранять без хеша
	if progress.StateHash == "" {
		return fmt.Errorf("state hash is empty")
	}

	// Для сцены с индексом 0 проверяем, действительно ли это сетап
	if sceneIndex == 0 {
		log.Printf("[Repo] Checking if state for scene 0 is setup. NovelID: %s, UserID: %s, Hash: %s",
			novelID, userID, progress.StateHash)

		// Получаем данные состояния по хешу, чтобы проверить current_stage
		stateData, err := r.GetNovelStateByHash(ctx, progress.StateHash)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				log.Printf("[Repo] SaveUserStoryProgress - Error getting state by hash %s: %v", progress.StateHash, err)
				// Не возвращаем ошибку, так как основная цель - сохранить прогресс
			} else {
				log.Printf("[Repo] SaveUserStoryProgress - State not found by hash %s. Cannot verify if it's setup.", progress.StateHash)
			}
		} else {
			// Проверяем current_stage
			var state struct {
				CurrentStage string `json:"current_stage"`
			}
			if err := json.Unmarshal(stateData, &state); err == nil {
				if state.CurrentStage == "setup" {
					log.Printf("[Repo] State for scene 0 is indeed setup. Saving to novels table. NovelID: %s", novelID)
					// Если это действительно сетап, сохраняем его в novels.setup_state_data
					err = r.SaveNovelSetupState(ctx, novelID, stateData)
					if err != nil {
						// Логируем ошибку, но не прерываем сохранение прогресса
						log.Printf("[Repo] SaveUserStoryProgress - Warning: failed to save setup state to novels table: %v", err)
					}
				} else {
					log.Printf("[Repo] SaveUserStoryProgress - State for scene 0 hash %s is not setup (current_stage: %s). Not saving to novels table.",
						progress.StateHash, state.CurrentStage)
				}
			} else {
				log.Printf("[Repo] SaveUserStoryProgress - Warning: failed to unmarshal state data for hash %s to check current_stage: %v",
					progress.StateHash, err)
			}
		}
	}

	// Сериализуем данные для JSONB полей
	globalFlagsJSON, err := json.Marshal(progress.GlobalFlags)
	if err != nil {
		return fmt.Errorf("failed to marshal global flags: %w", err)
	}

	relationshipJSON, err := json.Marshal(progress.Relationship)
	if err != nil {
		return fmt.Errorf("failed to marshal relationship: %w", err)
	}

	storyVariablesJSON, err := json.Marshal(progress.StoryVariables)
	if err != nil {
		return fmt.Errorf("failed to marshal story variables: %w", err)
	}

	previousChoicesJSON, err := json.Marshal(progress.PreviousChoices)
	if err != nil {
		return fmt.Errorf("failed to marshal previous choices: %w", err)
	}

	query := `
		INSERT INTO user_story_progress (
			novel_id, user_id, scene_index, global_flags, relationship, story_variables, 
			previous_choices, story_summary_so_far, future_direction, state_hash, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW()
		)
		ON CONFLICT (novel_id, user_id, scene_index) DO UPDATE 
		SET global_flags = EXCLUDED.global_flags,
			relationship = EXCLUDED.relationship,
			story_variables = EXCLUDED.story_variables,
			previous_choices = EXCLUDED.previous_choices,
			story_summary_so_far = EXCLUDED.story_summary_so_far,
			future_direction = EXCLUDED.future_direction,
			state_hash = EXCLUDED.state_hash,
			updated_at = NOW();
	`

	log.Printf("[Repo] Saving user story progress. NovelID: %s, SceneIndex: %d, UserID: %s, Hash: %s",
		novelID, sceneIndex, userID, progress.StateHash)

	_, err = r.db.Exec(ctx, query,
		novelID,
		userID,
		sceneIndex,
		globalFlagsJSON,
		relationshipJSON,
		storyVariablesJSON,
		previousChoicesJSON,
		progress.StorySummarySoFar,
		progress.FutureDirection,
		progress.StateHash)

	if err != nil {
		log.Printf("[Repo] Error saving user story progress: %v", err)
		return fmt.Errorf("failed to save user story progress: %w", err)
	}

	// Обновляем таблицу user_novel_progress для сохранения текущей сцены
	updateProgressQuery := `
		INSERT INTO user_novel_progress (novel_id, user_id, current_scene_index, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (novel_id, user_id) DO UPDATE
		SET current_scene_index = CASE
			WHEN $3 > user_novel_progress.current_scene_index THEN $3
			ELSE user_novel_progress.current_scene_index
		END,
		updated_at = NOW();
	`

	_, err = r.db.Exec(ctx, updateProgressQuery, novelID, userID, sceneIndex)
	if err != nil {
		log.Printf("[Repo] Warning: Could not update user progress: %v", err)
		// Не возвращаем ошибку, так как основное сохранение выполнено успешно
	}

	log.Printf("[Repo] Successfully saved user story progress. NovelID: %s, SceneIndex: %d, UserID: %s",
		novelID, sceneIndex, userID)

	return nil
}

// GetUserStoryProgressByHash возвращает прогресс истории по хешу состояния.
// Этот метод заменяет GetNovelStateByHash для новой схемы данных.
func (r *PostgresNovelRepository) GetUserStoryProgressByHash(ctx context.Context, stateHash string) (*domain.UserStoryProgress, error) {
	query := `
		SELECT 
			novel_id, user_id, scene_index, global_flags, relationship, story_variables,
			previous_choices, story_summary_so_far, future_direction, state_hash, created_at, updated_at
		FROM user_story_progress 
		WHERE state_hash = $1 
		LIMIT 1;
	`

	log.Printf("[Repo] Getting user story progress by hash. Hash: %s", stateHash)

	var progress domain.UserStoryProgress
	var globalFlagsJSON, relationshipJSON, storyVariablesJSON, previousChoicesJSON []byte

	err := r.db.QueryRow(ctx, query, stateHash).Scan(
		&progress.NovelID,
		&progress.UserID,
		&progress.SceneIndex,
		&globalFlagsJSON,
		&relationshipJSON,
		&storyVariablesJSON,
		&previousChoicesJSON,
		&progress.StorySummarySoFar,
		&progress.FutureDirection,
		&progress.StateHash,
		&progress.CreatedAt,
		&progress.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] No user story progress found by hash: %s", stateHash)
			return nil, pgx.ErrNoRows
		}
		log.Printf("[Repo] Error getting user story progress by hash: %v", err)
		return nil, fmt.Errorf("failed to get user story progress by hash: %w", err)
	}

	// Десериализуем JSONB поля
	if err := json.Unmarshal(globalFlagsJSON, &progress.GlobalFlags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal global flags: %w", err)
	}

	if err := json.Unmarshal(relationshipJSON, &progress.Relationship); err != nil {
		return nil, fmt.Errorf("failed to unmarshal relationship: %w", err)
	}

	if err := json.Unmarshal(storyVariablesJSON, &progress.StoryVariables); err != nil {
		return nil, fmt.Errorf("failed to unmarshal story variables: %w", err)
	}

	if err := json.Unmarshal(previousChoicesJSON, &progress.PreviousChoices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal previous choices: %w", err)
	}

	log.Printf("[Repo] Found user story progress by hash: %s, NovelID: %s, SceneIndex: %d, UserID: %s",
		stateHash, progress.NovelID, progress.SceneIndex, progress.UserID)

	return &progress, nil
}

// GetLatestUserStoryProgress возвращает последний сохраненный прогресс пользователя
// для конкретной новеллы. Возвращает nil и -1, если прогресс не найден.
func (r *PostgresNovelRepository) GetLatestUserStoryProgress(ctx context.Context, novelID uuid.UUID, userID string) (*domain.UserStoryProgress, int, error) {
	// Сначала получаем индекс последней сцены из таблицы user_novel_progress
	var currentSceneIndex int
	progressQuery := `
		SELECT current_scene_index 
		FROM user_novel_progress 
		WHERE novel_id = $1 AND user_id = $2 
		LIMIT 1;
	`

	log.Printf("[Repo] Getting latest user story progress. NovelID: %s, UserID: %s", novelID, userID)

	err := r.db.QueryRow(ctx, progressQuery, novelID, userID).Scan(&currentSceneIndex)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] No progress found for user. NovelID: %s, UserID: %s", novelID, userID)
			return nil, -1, nil
		}
		log.Printf("[Repo] Error getting user progress: %v", err)
		return nil, -1, fmt.Errorf("failed to get user progress: %w", err)
	}

	// Теперь получаем полные данные прогресса по последней сцене
	query := `
		SELECT 
			novel_id, user_id, scene_index, global_flags, relationship, story_variables,
			previous_choices, story_summary_so_far, future_direction, state_hash, created_at, updated_at
		FROM user_story_progress 
		WHERE novel_id = $1 AND user_id = $2 AND scene_index = $3 
		LIMIT 1;
	`

	var progress domain.UserStoryProgress
	var globalFlagsJSON, relationshipJSON, storyVariablesJSON, previousChoicesJSON []byte

	err = r.db.QueryRow(ctx, query, novelID, userID, currentSceneIndex).Scan(
		&progress.NovelID,
		&progress.UserID,
		&progress.SceneIndex,
		&globalFlagsJSON,
		&relationshipJSON,
		&storyVariablesJSON,
		&previousChoicesJSON,
		&progress.StorySummarySoFar,
		&progress.FutureDirection,
		&progress.StateHash,
		&progress.CreatedAt,
		&progress.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] No user story progress found for scene %d. NovelID: %s, UserID: %s",
				currentSceneIndex, novelID, userID)
			return nil, currentSceneIndex, nil
		}
		log.Printf("[Repo] Error getting user story progress: %v", err)
		return nil, -1, fmt.Errorf("failed to get user story progress: %w", err)
	}

	// Десериализуем JSONB поля
	if err := json.Unmarshal(globalFlagsJSON, &progress.GlobalFlags); err != nil {
		return nil, currentSceneIndex, fmt.Errorf("failed to unmarshal global flags: %w", err)
	}

	if err := json.Unmarshal(relationshipJSON, &progress.Relationship); err != nil {
		return nil, currentSceneIndex, fmt.Errorf("failed to unmarshal relationship: %w", err)
	}

	if err := json.Unmarshal(storyVariablesJSON, &progress.StoryVariables); err != nil {
		return nil, currentSceneIndex, fmt.Errorf("failed to unmarshal story variables: %w", err)
	}

	if err := json.Unmarshal(previousChoicesJSON, &progress.PreviousChoices); err != nil {
		return nil, currentSceneIndex, fmt.Errorf("failed to unmarshal previous choices: %w", err)
	}

	log.Printf("[Repo] Found latest user story progress. NovelID: %s, UserID: %s, SceneIndex: %d",
		novelID, userID, currentSceneIndex)

	return &progress, currentSceneIndex, nil
}

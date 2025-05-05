package database

import (
	"novel-server/shared/interfaces"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// --- ОБЩИЕ КОНСТАНТЫ (можно вынести в отдельный файл _constants.go) ---
// TODO: Consider moving these to a separate file or organizing better.
const (
	// Поля для полной структуры PublishedStory
	publishedStoryFields = `
		ps.id, ps.user_id, ps.config, ps.setup, ps.status, ps.language, ps.is_public, ps.is_adult_content,
		ps.title, ps.description, ps.cover_image_url, ps.error_details, ps.likes_count, ps.created_at, ps.updated_at,
		ps.is_first_scene_pending, ps.are_images_pending
	`
	// Поля для сводки (используются в PublishedStorySummary)
	publishedStorySummaryFields = `
		ps.id, ps.title, ps.description, ps.user_id, u.display_name as author_name, ps.created_at, ps.is_adult_content,
		ps.likes_count, (sl.user_id IS NOT NULL) as is_liked, ps.status, ir.image_url as cover_image_url
	`
	// Поля для сводки с прогрессом (используются в PublishedStorySummaryWithProgress)
	publishedStorySummaryWithProgressFields = publishedStorySummaryFields + `,
		(pgs.player_progress_id IS NOT NULL) as has_player_progress, ps.is_public, pgs.player_status
	`
)

// Compile-time check (will error until all methods are moved/implemented)
var _ interfaces.PublishedStoryRepository = (*pgPublishedStoryRepository)(nil)

// scanStories сканирует несколько строк (возвращает слайс указателей)
// << HELPER FUNCTION MOVED TO _helpers.go >>

// pgPublishedStoryRepository реализует интерфейс PublishedStoryRepository для PostgreSQL.
type pgPublishedStoryRepository struct {
	db     interfaces.DBTX
	logger *zap.Logger
}

// NewPgPublishedStoryRepository создает новый экземпляр репозитория.
func NewPgPublishedStoryRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.PublishedStoryRepository {
	return &pgPublishedStoryRepository{
		db:     db,
		logger: logger.Named("PgPublishedStoryRepo"),
	}
}

// Create создает новую запись опубликованной истории.
// << METHOD MOVED TO _crud.go >>

// GetByID retrieves a published story by its unique ID.
// << METHOD MOVED TO _crud.go >>

// UpdateStatusDetails обновляет статус, детали ошибки или setup опубликованной истории.
// << METHOD MOVED TO _status.go >>

// ListByUserID retrieves a paginated list of stories created by a specific user using cursor pagination.
// << METHOD MOVED TO _list.go >>

// UpdateVisibility updates the visibility of a story.
// << METHOD MOVED TO ??? (Needs categorization - maybe _misc.go or _status.go?) >>
// Let's put it in _status.go for now as it relates to readiness status.

// UpdateConfigAndSetupAndStatus updates config, setup and status for a published story.
// << METHOD MOVED TO _status.go >>

// CountActiveGenerationsForUser counts the number of published stories with statuses
// indicating active generation for a specific user.
// << METHOD MOVED TO _status.go >>

// ListLikedByUser получает пагинированный список историй, лайкнутых пользователем, используя курсор.
// Возвращает структуру PublishedStorySummaryWithProgress.
// << METHOD MOVED TO _list.go >>

// MarkStoryAsLiked отмечает историю как лайкнутую пользователем.
// Выполняется в транзакции: добавляет запись в user_story_likes и инкрементирует счетчик.
// << METHOD MOVED TO _likes.go >>

// MarkStoryAsUnliked отмечает историю как не лайкнутую пользователем.
// Выполняется в транзакции: удаляет запись из user_story_likes и декрементирует счетчик.
// << METHOD MOVED TO _likes.go >>

// Ensure pgPublishedStoryRepository implements PublishedStoryRepository
var _ interfaces.PublishedStoryRepository = (*pgPublishedStoryRepository)(nil)

// Delete удаляет опубликованную историю и все связанные с ней данные.
// << METHOD MOVED TO _misc.go >>

// FindWithProgressByUserID retrieves a paginated list of stories with progress for a specific user using cursor pagination.
// << METHOD MOVED TO _progress.go >>

// CheckLike checks if a user has liked a story.
// << METHOD MOVED TO _likes.go >>

// CountByStatus подсчитывает количество опубликованных историй по заданному статусу.
// << METHOD MOVED TO _status.go >>

// FindAndMarkStaleGeneratingAsError находит "зависшие" истории в процессе генерации и устанавливает им статус Error.
// << METHOD MOVED TO _status.go >>

// CheckInitialGenerationStatus проверяет, готовы ли Setup и Первая сцена (проверяя статус).
// << METHOD MOVED TO _status.go >>

// GetConfigAndSetup получает Config и Setup по ID истории.
// << METHOD MOVED TO _crud.go >>

// --- Вспомогательная функция для логирования UUID (может быть nil) ---
func uuidToStringPtrLog(id *uuid.UUID) string {
	if id == nil {
		return "<nil>"
	}
	return id.String()
}

// ListPublicSummaries получает список публичных историй с пагинацией.
// Возвращает PublishedStorySummaryWithProgress, чтобы включать is_liked, is_public и has_player_progress.
// << METHOD MOVED TO _list.go >>

// ListUserSummariesWithProgress получает список историй пользователя с прогрессом.
// << METHOD MOVED TO _progress.go >>

// GetSummaryWithDetails получает детали истории, имя автора, флаг лайка и прогресса для указанного пользователя.
// << METHOD MOVED TO _progress.go >>

// ListUserSummariesOnlyWithProgress получает список историй, в которых у пользователя есть прогресс,
// сортируя их по времени последней активности (сначала новые).
// << METHOD MOVED TO _progress.go >>

// UpdateStatusFlagsAndSetup обновляет статус, флаги и setup для опубликованной истории.
// << METHOD MOVED TO _status.go >>

// UpdateStatusFlagsAndDetails обновляет статус, флаги и детали ошибки для опубликованной истории.
// << METHOD MOVED TO _status.go >>

// Update обновляет данные опубликованной истории.
// << METHOD MOVED TO _crud.go >>

// SearchPublic выполняет полнотекстовый поиск по общедоступным историям.
// << METHOD MOVED TO _list.go >>

// DeleteByID удаляет опубликованную историю по ID.
// << METHOD MOVED TO _misc.go >>

// IncrementViewCount увеличивает счетчик просмотров для истории.
// << METHOD MOVED TO _misc.go >>

// UpdateLikeCount обновляет счетчик лайков для истории (используется после добавления/удаления лайка).
// << METHOD MOVED TO _likes.go >>

// --- Helper Scan Functions ---
// << HELPERS MOVED TO _helpers.go >>

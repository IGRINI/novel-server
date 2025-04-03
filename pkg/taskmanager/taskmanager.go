package taskmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// ITaskManager определяет интерфейс для управления задачами
type ITaskManager interface {
	SubmitTask(ctx context.Context, taskFunc TaskFunc, params interface{}) (uuid.UUID, error)
	SubmitTaskWithOwner(ctx context.Context, taskFunc TaskFunc, params interface{}, ownerID string) (uuid.UUID, error)
	GetTask(taskID uuid.UUID) (*Task, error)
	Close()
	Shutdown(ctx context.Context) error
	CancelTask(taskID uuid.UUID) error
	RegisterCallback(taskID uuid.UUID, callback TaskCallback) error
	UnregisterCallbacks(taskID uuid.UUID)
	CleanupTasks(age time.Duration)
	SetWebSocketNotifier(notifier WebSocketNotifier)
}

// WebSocketNotifier интерфейс для отправки уведомлений через WebSocket
type WebSocketNotifier interface {
	SendToUser(userID, messageType, topic string, payload interface{})
	Broadcast(messageType, topic string, payload interface{})
}

// NewManager создает новый экземпляр TaskManager с настройками по умолчанию
func NewManager() *TaskManager {
	manager, _ := New(Config{MaxTasks: 10})
	return manager
}

// Task представляет асинхронную задачу
type Task struct {
	ID        uuid.UUID
	Status    TaskStatus
	Progress  int
	Message   string
	Result    interface{}
	CreatedAt time.Time
	UpdatedAt time.Time
	Cancel    context.CancelFunc
}

// TaskStatus представляет статус задачи
type TaskStatus string

// Возможные статусы задач
const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// TaskFunc представляет функцию, выполняемую в задаче
type TaskFunc func(ctx context.Context, params interface{}) (interface{}, error)

// TaskManager управляет асинхронными задачами
type TaskManager struct {
	tasks      map[uuid.UUID]*Task
	mu         sync.RWMutex
	maxTasks   int
	callbacks  map[uuid.UUID][]TaskCallback
	closing    chan struct{}
	wg         sync.WaitGroup
	wsNotifier WebSocketNotifier
	taskOwners map[uuid.UUID]string // Маппинг taskID -> userID
}

// TaskCallback представляет функцию обратного вызова, вызываемую при изменении статуса задачи
type TaskCallback func(task *Task)

// Config содержит конфигурацию для TaskManager
type Config struct {
	MaxTasks int
}

// New создает новый экземпляр TaskManager
func New(cfg Config) (*TaskManager, error) {
	maxTasks := cfg.MaxTasks
	if maxTasks <= 0 {
		maxTasks = 10
	}

	return &TaskManager{
		tasks:      make(map[uuid.UUID]*Task),
		maxTasks:   maxTasks,
		callbacks:  make(map[uuid.UUID][]TaskCallback),
		closing:    make(chan struct{}),
		taskOwners: make(map[uuid.UUID]string),
	}, nil
}

// Close закрывает менеджер задач и отменяет все незавершенные задачи
func (tm *TaskManager) Close() {
	close(tm.closing)
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Отменяем все задачи
	for _, task := range tm.tasks {
		if task.Status == TaskStatusPending || task.Status == TaskStatusRunning {
			if task.Cancel != nil {
				task.Cancel()
			}
		}
	}

	// Ждем завершения всех горутин
	tm.wg.Wait()
}

// Shutdown ожидает завершения всех задач с таймаутом
func (tm *TaskManager) Shutdown(ctx context.Context) error {
	close(tm.closing)

	// Создаем канал для сигнала о завершении всех задач
	done := make(chan struct{})
	go func() {
		tm.wg.Wait()
		close(done)
	}()

	// Ожидаем либо завершения всех задач, либо таймаута
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("таймаут при ожидании завершения задач")
	}
}

// SubmitTask создает и запускает новую задачу
func (tm *TaskManager) SubmitTask(ctx context.Context, taskFunc TaskFunc, params interface{}) (uuid.UUID, error) {
	tm.mu.Lock()         // Блокируем в начале
	defer tm.mu.Unlock() // Гарантируем разблокировку в конце

	// Проверка maxTasks (под блокировкой)
	activeTasks := 0
	for _, task := range tm.tasks {
		if task.Status == TaskStatusPending || task.Status == TaskStatusRunning {
			activeTasks++
		}
	}
	if activeTasks >= tm.maxTasks {
		// tm.mu.Unlock() // Больше не нужно, defer сделает это
		return uuid.UUID{}, errors.New("превышено максимальное количество активных задач")
	}
	// tm.mu.Unlock() // УДАЛЯЕМ преждевременную разблокировку

	// Создаем новую задачу
	taskID := uuid.New()

	// --- Создание независимого контекста с логгером ---
	baseTaskCtx, cancel := context.WithCancel(context.Background())
	taskLogger := log.Ctx(ctx) // Получаем логгер zerolog из ctx
	taskCtx := taskLogger.WithContext(baseTaskCtx)
	// ----------------------

	task := &Task{
		ID:        taskID,
		Status:    TaskStatusPending,
		Progress:  0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Cancel:    cancel, // Сохраняем функцию отмены для taskCtx
	}

	// Добавляем задачу в map (под блокировкой)
	// tm.mu.Lock() // Больше не нужно, блокировка уже есть
	tm.tasks[taskID] = task
	// tm.mu.Unlock() // Больше не нужно, defer сделает это

	// Блокировка будет снята здесь с помощью defer

	// Запускаем задачу в отдельной горутине
	tm.wg.Add(1)
	go func() {
		defer tm.wg.Done()
		defer cancel()

		tm.runTask(taskCtx, task, taskFunc, params)
	}()

	return taskID, nil
}

// runTask выполняет задачу и обновляет ее статус
func (tm *TaskManager) runTask(ctx context.Context, task *Task, taskFunc TaskFunc, params interface{}) {
	tm.updateTaskStatus(ctx, task, TaskStatusRunning, 0, "Задача запущена")

	result, err := taskFunc(ctx, params)

	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			log.Ctx(ctx).Info().Str("taskID", task.ID.String()).Msg("Контекст задачи был отменен")
			tm.updateTaskStatus(ctx, task, TaskStatusCancelled, 100, "Задача отменена")
		} else {
			log.Ctx(ctx).Error().Err(ctx.Err()).Str("taskID", task.ID.String()).Msg("Ошибка контекста задачи")
			tm.updateTaskStatus(ctx, task, TaskStatusFailed, 100, fmt.Sprintf("Ошибка контекста: %v", ctx.Err()))
		}
		return
	}

	if err != nil {
		log.Ctx(ctx).Error().Err(err).Str("taskID", task.ID.String()).Msg("Задача завершилась с ошибкой")
		tm.updateTaskStatus(ctx, task, TaskStatusFailed, 100, fmt.Sprintf("Ошибка: %v", err))
	} else {
		task.Result = result
		log.Ctx(ctx).Info().Str("taskID", task.ID.String()).Msg("Задача успешно выполнена")
		tm.updateTaskStatus(ctx, task, TaskStatusCompleted, 100, "Задача успешно выполнена")
	}
}

// updateTaskStatus обновляет статус задачи и отправляет уведомления
func (tm *TaskManager) updateTaskStatus(ctx context.Context, task *Task, status TaskStatus, progress int, message string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task.Status = status
	task.Progress = progress
	task.Message = message
	task.UpdatedAt = time.Now()

	if callbacks, ok := tm.callbacks[task.ID]; ok {
		for _, callback := range callbacks {
			go callback(task)
		}
	}

	if tm.wsNotifier != nil {
		payload := map[string]interface{}{
			"task_id":    task.ID,
			"status":     task.Status,
			"progress":   task.Progress,
			"message":    task.Message,
			"updated_at": task.UpdatedAt,
		}

		if task.Status == TaskStatusCompleted && task.Result != nil {
			payload["result"] = task.Result
		}

		if ownerID, ok := tm.taskOwners[task.ID]; ok {
			tm.wsNotifier.SendToUser(ownerID, "task_update", "tasks", payload)
		}
	}

	log.Ctx(ctx).Info().
		Str("taskID", task.ID.String()).
		Str("newStatus", string(task.Status)).
		Int("progress", task.Progress).
		Str("message", task.Message).
		Msg("Статус задачи обновлен")
}

// GetTask возвращает информацию о задаче по ID
func (tm *TaskManager) GetTask(taskID uuid.UUID) (*Task, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	task, ok := tm.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("задача с ID %s не найдена", taskID)
	}

	return task, nil
}

// CancelTask отменяет выполнение задачи
func (tm *TaskManager) CancelTask(taskID uuid.UUID) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, ok := tm.tasks[taskID]
	if !ok {
		return fmt.Errorf("задача с ID %s не найдена", taskID)
	}

	if task.Status != TaskStatusPending && task.Status != TaskStatusRunning {
		return fmt.Errorf("невозможно отменить задачу в статусе %s", task.Status)
	}

	if task.Cancel != nil {
		task.Cancel()
	}

	task.Status = TaskStatusCancelled
	task.Message = "Задача отменена пользователем"
	task.UpdatedAt = time.Now()

	return nil
}

// RegisterCallback регистрирует функцию обратного вызова для задачи
func (tm *TaskManager) RegisterCallback(taskID uuid.UUID, callback TaskCallback) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, ok := tm.tasks[taskID]; !ok {
		return fmt.Errorf("задача с ID %s не найдена", taskID)
	}

	if _, ok := tm.callbacks[taskID]; !ok {
		tm.callbacks[taskID] = make([]TaskCallback, 0)
	}

	tm.callbacks[taskID] = append(tm.callbacks[taskID], callback)
	return nil
}

// UnregisterCallback удаляет все коллбэки для задачи
func (tm *TaskManager) UnregisterCallbacks(taskID uuid.UUID) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	delete(tm.callbacks, taskID)
}

// CleanupTasks удаляет завершенные задачи, которые старше указанного времени
func (tm *TaskManager) CleanupTasks(age time.Duration) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	for id, task := range tm.tasks {
		if (task.Status == TaskStatusCompleted || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled) &&
			now.Sub(task.UpdatedAt) > age {
			delete(tm.tasks, id)
			delete(tm.callbacks, id)
		}
	}
}

// SetWebSocketNotifier устанавливает WebSocket нотификатор
func (tm *TaskManager) SetWebSocketNotifier(notifier WebSocketNotifier) {
	tm.wsNotifier = notifier
}

// SubmitTaskWithOwner создает и запускает новую задачу с указанием владельца
func (tm *TaskManager) SubmitTaskWithOwner(ctx context.Context, taskFunc TaskFunc, params interface{}, ownerID string) (uuid.UUID, error) {
	taskID, err := tm.SubmitTask(ctx, taskFunc, params)
	if err != nil {
		return uuid.UUID{}, err
	}

	tm.mu.Lock()
	tm.taskOwners[taskID] = ownerID
	tm.mu.Unlock()

	return taskID, nil
}

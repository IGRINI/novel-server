package taskmanager

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
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
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Проверяем количество активных задач
	activeTasks := 0
	for _, task := range tm.tasks {
		if task.Status == TaskStatusPending || task.Status == TaskStatusRunning {
			activeTasks++
		}
	}

	if activeTasks >= tm.maxTasks {
		return uuid.UUID{}, errors.New("превышено максимальное количество активных задач")
	}

	// Создаем новую задачу
	taskID := uuid.New()
	taskCtx, cancel := context.WithCancel(ctx)

	task := &Task{
		ID:        taskID,
		Status:    TaskStatusPending,
		Progress:  0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Cancel:    cancel,
	}

	tm.tasks[taskID] = task

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
	// Обновляем статус задачи на "выполняется"
	tm.updateTaskStatus(task, TaskStatusRunning, 0, "Задача запущена")

	// Выполняем задачу
	result, err := taskFunc(ctx, params)

	// Проверяем, не был ли контекст отменен
	if ctx.Err() != nil {
		tm.updateTaskStatus(task, TaskStatusCancelled, 100, "Задача отменена")
		return
	}

	// Обновляем статус задачи в зависимости от результата
	if err != nil {
		tm.updateTaskStatus(task, TaskStatusFailed, 100, fmt.Sprintf("Ошибка: %v", err))
	} else {
		task.Result = result
		tm.updateTaskStatus(task, TaskStatusCompleted, 100, "Задача успешно выполнена")
	}
}

// updateTaskStatus обновляет статус задачи и отправляет уведомления
func (tm *TaskManager) updateTaskStatus(task *Task, status TaskStatus, progress int, message string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task.Status = status
	task.Progress = progress
	task.Message = message
	task.UpdatedAt = time.Now()

	// Вызываем коллбэки, если они есть
	if callbacks, ok := tm.callbacks[task.ID]; ok {
		for _, callback := range callbacks {
			go callback(task)
		}
	}

	// Отправляем уведомление через WebSocket, если нотификатор установлен
	if tm.wsNotifier != nil {
		// Создаем payload для уведомления
		payload := map[string]interface{}{
			"task_id":    task.ID,
			"status":     task.Status,
			"progress":   task.Progress,
			"message":    task.Message,
			"updated_at": task.UpdatedAt,
		}

		// Если есть результат и задача завершена, добавляем его
		if task.Status == TaskStatusCompleted && task.Result != nil {
			payload["result"] = task.Result
		}

		// Если известен владелец задачи, отправляем уведомление конкретному пользователю
		if ownerID, ok := tm.taskOwners[task.ID]; ok {
			tm.wsNotifier.SendToUser(ownerID, "task_update", "tasks", payload)
		}
	}

	log.Printf("Задача %s: статус изменен на %s, прогресс: %d%%, сообщение: %s",
		task.ID, task.Status, task.Progress, task.Message)
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

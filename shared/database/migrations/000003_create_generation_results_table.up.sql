-- +migrate Up
-- Up Migration: Create generation_results table

CREATE TABLE IF NOT EXISTS generation_results (
    id TEXT PRIMARY KEY,                     -- ID задачи (TaskID)
    user_id VARCHAR(255) NOT NULL,           -- ID пользователя
    prompt_type VARCHAR(100) NOT NULL,       -- Тип промта (e.g., 'novel_setup')
    input_data JSONB NOT NULL DEFAULT '{}',  -- Входные данные задачи (JSONB)
    generated_text TEXT,                     -- Сгенерированный текст
    processing_time_ms BIGINT NOT NULL,      -- Время обработки в миллисекундах
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- Время получения задачи
    completed_at TIMESTAMPTZ NOT NULL,       -- Время завершения обработки
    error TEXT                               -- Текст ошибки, если была
);

-- Индексы для ускорения запросов
CREATE INDEX IF NOT EXISTS idx_generation_results_user_id ON generation_results (user_id);
CREATE INDEX IF NOT EXISTS idx_generation_results_created_at ON generation_results (created_at);
CREATE INDEX IF NOT EXISTS idx_generation_results_prompt_type ON generation_results (prompt_type);

COMMENT ON TABLE generation_results IS 'Хранит результаты выполнения задач генерации текста AI.';
COMMENT ON COLUMN generation_results.id IS 'Уникальный идентификатор задачи генерации (обычно совпадает с TaskID из сообщения).';
COMMENT ON COLUMN generation_results.user_id IS 'Идентификатор пользователя, инициировавшего задачу.';
COMMENT ON COLUMN generation_results.prompt_type IS 'Тип промта, использованного для генерации.';
COMMENT ON COLUMN generation_results.input_data IS 'Входные данные (параметры) для шаблона промта в формате JSONB.';
COMMENT ON COLUMN generation_results.generated_text IS 'Текст, сгенерированный AI.';
COMMENT ON COLUMN generation_results.processing_time_ms IS 'Время выполнения AI-запроса и внутренней обработки в миллисекундах.';
COMMENT ON COLUMN generation_results.created_at IS 'Время получения задачи воркером.';
COMMENT ON COLUMN generation_results.completed_at IS 'Время завершения обработки задачи воркером.';
COMMENT ON COLUMN generation_results.error IS 'Текст ошибки, возникшей при обработке задачи (если была).'; 
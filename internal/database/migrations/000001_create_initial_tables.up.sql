-- Таблица пользователей
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    username VARCHAR(50) NOT NULL UNIQUE,
    email VARCHAR(100) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Таблица новелл
CREATE TABLE IF NOT EXISTS novels (
    id UUID PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    author_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_public BOOLEAN NOT NULL DEFAULT FALSE,
    cover_image VARCHAR(255),
    tags JSONB NOT NULL DEFAULT '[]'::JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    published_at TIMESTAMP
);

-- Таблица сцен новеллы
CREATE TABLE IF NOT EXISTS scenes (
    id UUID PRIMARY KEY,
    novel_id UUID NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    content TEXT NOT NULL,
    "order" INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Таблица вариантов выбора для сцен
CREATE TABLE IF NOT EXISTS choices (
    id UUID PRIMARY KEY,
    scene_id UUID NOT NULL REFERENCES scenes(id) ON DELETE CASCADE,
    text TEXT NOT NULL,
    next_scene_id UUID REFERENCES scenes(id) ON DELETE SET NULL,
    requirements TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Таблица состояний новеллы для игроков
CREATE TABLE IF NOT EXISTS novel_states (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    novel_id UUID NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    current_scene_id UUID REFERENCES scenes(id) ON DELETE SET NULL,
    variables JSONB NOT NULL DEFAULT '{}'::JSONB,
    history JSONB NOT NULL DEFAULT '[]'::JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, novel_id)
);

-- Индексы для оптимизации запросов
CREATE INDEX IF NOT EXISTS idx_novels_author_id ON novels(author_id);
CREATE INDEX IF NOT EXISTS idx_novels_is_public ON novels(is_public);
CREATE INDEX IF NOT EXISTS idx_scenes_novel_id ON scenes(novel_id);
CREATE INDEX IF NOT EXISTS idx_scenes_order ON scenes("order");
CREATE INDEX IF NOT EXISTS idx_choices_scene_id ON choices(scene_id);
-- CREATE INDEX IF NOT EXISTS idx_characters_novel_id ON characters(novel_id); -- Таблица characters удалена
CREATE INDEX IF NOT EXISTS idx_novel_states_user_id ON novel_states(user_id);
CREATE INDEX IF NOT EXISTS idx_novel_states_novel_id ON novel_states(novel_id); 
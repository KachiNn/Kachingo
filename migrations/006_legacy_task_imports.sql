CREATE TABLE legacy_task_imports (
    source_sha256 TEXT NOT NULL,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    actor_id INTEGER NOT NULL REFERENCES users(id),
    task_count INTEGER NOT NULL CHECK (task_count >= 0),
    imported_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source_sha256, project_id)
);

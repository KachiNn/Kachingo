ALTER TABLE project_tasks ADD COLUMN assignee_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE project_tasks ADD COLUMN due_date TEXT;
CREATE INDEX project_tasks_assignee_idx ON project_tasks(project_id, assignee_id, id);
CREATE INDEX project_tasks_due_date_idx ON project_tasks(project_id, due_date, id);

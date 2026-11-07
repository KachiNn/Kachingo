package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var errTaskVersionConflict = errors.New("task version conflict")

type projectTaskRepository interface {
	List(context.Context, int64, string, string, int, int) (projectTaskPage, error)
	Create(context.Context, int64, string, string, string) (projectTask, error)
	Get(context.Context, int64, int64) (projectTask, error)
	Update(context.Context, projectTask, int64) (projectTask, error)
	Delete(context.Context, int64, int64) (bool, error)
}

type sqliteProjectTaskRepository struct{ db *sql.DB }

func (repo sqliteProjectTaskRepository) List(ctx context.Context, projectID int64, status, query string, limit, offset int) (projectTaskPage, error) {
	pattern := "%" + query + "%"
	var page projectTaskPage
	page.Limit, page.Offset = limit, offset
	if err := repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_tasks WHERE project_id=? AND (?='' OR status=?) AND title LIKE ?", projectID, status, status, pattern).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := repo.db.QueryContext(ctx, "SELECT id,project_id,title,description,status,priority,created_at,updated_at,version FROM project_tasks WHERE project_id=? AND (?='' OR status=?) AND title LIKE ? ORDER BY id LIMIT ? OFFSET ?", projectID, status, status, pattern, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	page.Items = make([]projectTask, 0)
	for rows.Next() {
		var task projectTask
		if err := scanProjectTask(rows, &task); err != nil {
			return page, err
		}
		page.Items = append(page.Items, task)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	return page, nil
}

func (repo sqliteProjectTaskRepository) Create(ctx context.Context, projectID int64, title, description, priority string) (projectTask, error) {
	result, err := repo.db.ExecContext(ctx, "INSERT INTO project_tasks(project_id,title,description,priority) VALUES(?,?,?,?)", projectID, title, description, priority)
	if err != nil {
		return projectTask{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return projectTask{}, err
	}
	return repo.Get(ctx, projectID, id)
}

func (repo sqliteProjectTaskRepository) Get(ctx context.Context, projectID, taskID int64) (projectTask, error) {
	var task projectTask
	err := scanProjectTask(repo.db.QueryRowContext(ctx, "SELECT id,project_id,title,description,status,priority,created_at,updated_at,version FROM project_tasks WHERE id=? AND project_id=?", taskID, projectID), &task)
	return task, err
}

func (repo sqliteProjectTaskRepository) Update(ctx context.Context, task projectTask, expectedVersion int64) (projectTask, error) {
	result, err := repo.db.ExecContext(ctx, "UPDATE project_tasks SET title=?,description=?,status=?,priority=?,updated_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=? AND project_id=? AND version=?", task.Title, task.Description, task.Status, task.Priority, task.ID, task.ProjectID, expectedVersion)
	if err != nil {
		return projectTask{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return projectTask{}, err
	}
	if updated == 0 {
		_, getErr := repo.Get(ctx, task.ProjectID, task.ID)
		if errors.Is(getErr, sql.ErrNoRows) {
			return projectTask{}, sql.ErrNoRows
		}
		if getErr != nil {
			return projectTask{}, getErr
		}
		return projectTask{}, errTaskVersionConflict
	}
	return repo.Get(ctx, task.ProjectID, task.ID)
}

func (repo sqliteProjectTaskRepository) Delete(ctx context.Context, projectID, taskID int64) (bool, error) {
	result, err := repo.db.ExecContext(ctx, "DELETE FROM project_tasks WHERE id=? AND project_id=?", taskID, projectID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

type rowScanner interface{ Scan(...any) error }

func scanProjectTask(row rowScanner, task *projectTask) error {
	if err := row.Scan(&task.ID, &task.ProjectID, &task.Title, &task.Description, &task.Status, &task.Priority, &task.CreatedAt, &task.UpdatedAt, &task.Version); err != nil {
		return fmt.Errorf("scan project task: %w", err)
	}
	return nil
}

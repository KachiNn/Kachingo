package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	errTaskVersionConflict      = errors.New("task version conflict")
	errAssigneeNotProjectMember = errors.New("assignee is not a project member")
)

type projectTaskRepository interface {
	List(context.Context, int64, string, string, int64, bool, bool, int, int) (projectTaskPage, error)
	Create(context.Context, int64, int64, string, string, string, *int64, *string) (projectTask, error)
	Get(context.Context, int64, int64) (projectTask, error)
	Update(context.Context, projectTask, int64, int64, string) (projectTask, error)
	Events(context.Context, int64, int64) ([]taskEvent, error)
	Delete(context.Context, int64, int64) (bool, error)
}
type sqliteProjectTaskRepository struct{ db *sql.DB }

func (repo sqliteProjectTaskRepository) List(ctx context.Context, projectID int64, status, query string, assigneeID int64, unassigned, overdue bool, limit, offset int) (projectTaskPage, error) {
	pattern := "%" + query + "%"
	var page projectTaskPage
	page.Limit, page.Offset = limit, offset
	filters := "project_id=? AND (?='' OR status=?) AND title LIKE ? AND (?=0 OR assignee_id=?) AND (?=0 OR assignee_id IS NULL) AND (?=0 OR (due_date < date('now') AND status != 'done'))"
	args := []any{projectID, status, status, pattern, boolInt(assigneeID > 0), assigneeID, boolInt(unassigned), boolInt(overdue)}
	if err := repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_tasks WHERE "+filters, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := repo.db.QueryContext(ctx, "SELECT id,project_id,title,description,status,priority,created_at,updated_at,version,assignee_id,due_date FROM project_tasks WHERE "+filters+" ORDER BY due_date IS NULL,due_date,id LIMIT ? OFFSET ?", append(args, limit, offset)...)
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
func (repo sqliteProjectTaskRepository) Create(ctx context.Context, projectID, actorID int64, title, description, priority string, assigneeID *int64, dueDate *string) (projectTask, error) {
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		return projectTask{}, err
	}
	defer tx.Rollback()
	if err := ensureProjectAssignee(ctx, tx, projectID, assigneeID); err != nil {
		return projectTask{}, err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO project_tasks(project_id,title,description,priority,assignee_id,due_date) VALUES(?,?,?,?,?,?)", projectID, title, description, priority, assigneeID, dueDate)
	if err != nil {
		return projectTask{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return projectTask{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO task_events(project_id,task_id,actor_id,event_type,to_status) VALUES(?,?,?,'created','open')", projectID, id, actorID); err != nil {
		return projectTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return projectTask{}, err
	}
	return repo.Get(ctx, projectID, id)
}
func (repo sqliteProjectTaskRepository) Get(ctx context.Context, projectID, taskID int64) (projectTask, error) {
	var task projectTask
	err := scanProjectTask(repo.db.QueryRowContext(ctx, "SELECT id,project_id,title,description,status,priority,created_at,updated_at,version,assignee_id,due_date FROM project_tasks WHERE id=? AND project_id=?", taskID, projectID), &task)
	return task, err
}
func (repo sqliteProjectTaskRepository) Update(ctx context.Context, task projectTask, expectedVersion, actorID int64, previousStatus string) (projectTask, error) {
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		return projectTask{}, err
	}
	defer tx.Rollback()
	if err := ensureProjectAssignee(ctx, tx, task.ProjectID, task.AssigneeID); err != nil {
		return projectTask{}, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE project_tasks SET title=?,description=?,status=?,priority=?,assignee_id=?,due_date=?,updated_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=? AND project_id=? AND version=?", task.Title, task.Description, task.Status, task.Priority, task.AssigneeID, task.DueDate, task.ID, task.ProjectID, expectedVersion)
	if err != nil {
		return projectTask{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return projectTask{}, err
	}
	if count == 0 {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return projectTask{}, err
		}
		_, getErr := repo.Get(ctx, task.ProjectID, task.ID)
		if errors.Is(getErr, sql.ErrNoRows) {
			return projectTask{}, sql.ErrNoRows
		}
		if getErr != nil {
			return projectTask{}, getErr
		}
		return projectTask{}, errTaskVersionConflict
	}
	if task.Status != previousStatus {
		if _, err := tx.ExecContext(ctx, "INSERT INTO task_events(project_id,task_id,actor_id,event_type,from_status,to_status) VALUES(?,?,?,'status_changed',?,?)", task.ProjectID, task.ID, actorID, previousStatus, task.Status); err != nil {
			return projectTask{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return projectTask{}, err
	}
	return repo.Get(ctx, task.ProjectID, task.ID)
}
func (repo sqliteProjectTaskRepository) Events(ctx context.Context, projectID, taskID int64) ([]taskEvent, error) {
	rows, err := repo.db.QueryContext(ctx, "SELECT id,task_id,actor_id,event_type,from_status,to_status,created_at FROM task_events WHERE project_id=? AND task_id=? ORDER BY id", projectID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]taskEvent, 0)
	for rows.Next() {
		var event taskEvent
		if err := rows.Scan(&event.ID, &event.TaskID, &event.ActorID, &event.EventType, &event.FromStatus, &event.ToStatus, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (repo sqliteProjectTaskRepository) Delete(ctx context.Context, projectID, taskID int64) (bool, error) {
	result, err := repo.db.ExecContext(ctx, "DELETE FROM project_tasks WHERE id=? AND project_id=?", taskID, projectID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}
func ensureProjectAssignee(ctx context.Context, tx *sql.Tx, projectID int64, assigneeID *int64) error {
	if assigneeID == nil {
		return nil
	}
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM project_members WHERE project_id=? AND user_id=?)", projectID, *assigneeID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return errAssigneeNotProjectMember
	}
	return nil
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type rowScanner interface{ Scan(...any) error }

func scanProjectTask(row rowScanner, task *projectTask) error {
	if err := row.Scan(&task.ID, &task.ProjectID, &task.Title, &task.Description, &task.Status, &task.Priority, &task.CreatedAt, &task.UpdatedAt, &task.Version, &task.AssigneeID, &task.DueDate); err != nil {
		return fmt.Errorf("scan project task: %w", err)
	}
	return nil
}

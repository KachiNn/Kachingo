package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	errEmailAlreadyRegistered = errors.New("email already registered")
	errMemberAlreadyAdded     = errors.New("member already added")
	errProjectOwnerProtected  = errors.New("project owner cannot be removed")
)

type authRepository interface {
	CreateUser(context.Context, string, string, string) (identity, error)
	AccountByEmail(context.Context, string) (identity, string, error)
	CreateSession(context.Context, int64, string, time.Time) error
	UserForSession(context.Context, string, time.Time) (identity, error)
	RevokeSession(context.Context, string) error
}

type projectRepository interface {
	ListForUser(context.Context, int64) ([]project, error)
	Create(context.Context, int64, string) (project, error)
	Role(context.Context, int64, int64) (string, error)
	UserByEmail(context.Context, string) (identity, error)
	AddMember(context.Context, int64, int64, string) error
	ListMembers(context.Context, int64) ([]projectMember, error)
	RemoveMember(context.Context, int64, int64) (bool, error)
}

type sqliteAuthRepository struct{ db *sql.DB }

func (repo sqliteAuthRepository) CreateUser(ctx context.Context, email, name, passwordHash string) (identity, error) {
	result, err := repo.db.ExecContext(ctx, "INSERT INTO users(email,name,password_hash) VALUES(?,?,?) ON CONFLICT(email) DO NOTHING", email, name, passwordHash)
	if err != nil {
		return identity{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return identity{}, err
	}
	if count == 0 {
		return identity{}, errEmailAlreadyRegistered
	}
	id, err := result.LastInsertId()
	return identity{ID: id, Email: email, Name: name}, err
}
func (repo sqliteAuthRepository) AccountByEmail(ctx context.Context, email string) (identity, string, error) {
	var user identity
	var hash string
	err := repo.db.QueryRowContext(ctx, "SELECT id,email,name,password_hash FROM users WHERE email=?", email).Scan(&user.ID, &user.Email, &user.Name, &hash)
	return user, hash, err
}
func (repo sqliteAuthRepository) CreateSession(ctx context.Context, userID int64, digest string, expires time.Time) error {
	_, err := repo.db.ExecContext(ctx, "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)", digest, userID, expires.UTC().Format(time.RFC3339Nano))
	return err
}
func (repo sqliteAuthRepository) UserForSession(ctx context.Context, digest string, now time.Time) (identity, error) {
	var user identity
	err := repo.db.QueryRowContext(ctx, "SELECT u.id,u.email,u.name FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?", digest, now.UTC().Format(time.RFC3339Nano)).Scan(&user.ID, &user.Email, &user.Name)
	return user, err
}
func (repo sqliteAuthRepository) RevokeSession(ctx context.Context, digest string) error {
	_, err := repo.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", digest)
	return err
}

type sqliteProjectRepository struct{ db *sql.DB }

func (repo sqliteProjectRepository) ListForUser(ctx context.Context, userID int64) ([]project, error) {
	rows, err := repo.db.QueryContext(ctx, "SELECT p.id,p.name,m.role,p.created_at FROM projects p JOIN project_members m ON m.project_id=p.id WHERE m.user_id=? ORDER BY p.id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]project, 0)
	for rows.Next() {
		var p project
		if err := rows.Scan(&p.ID, &p.Name, &p.Role, &p.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
func (repo sqliteProjectRepository) Create(ctx context.Context, userID int64, name string) (project, error) {
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		return project{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "INSERT INTO projects(name,owner_id) VALUES(?,?)", name, userID)
	if err != nil {
		return project{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return project{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO project_members(project_id,user_id,role) VALUES(?,?,'owner')", id, userID); err != nil {
		return project{}, err
	}
	if err = tx.Commit(); err != nil {
		return project{}, err
	}
	var p project
	err = repo.db.QueryRowContext(ctx, "SELECT id,name,created_at FROM projects WHERE id=?", id).Scan(&p.ID, &p.Name, &p.CreatedAt)
	p.Role = "owner"
	return p, err
}
func (repo sqliteProjectRepository) Role(ctx context.Context, projectID, userID int64) (string, error) {
	var role string
	err := repo.db.QueryRowContext(ctx, "SELECT role FROM project_members WHERE project_id=? AND user_id=?", projectID, userID).Scan(&role)
	return role, err
}
func (repo sqliteProjectRepository) UserByEmail(ctx context.Context, email string) (identity, error) {
	var user identity
	err := repo.db.QueryRowContext(ctx, "SELECT id,email,name FROM users WHERE email=?", email).Scan(&user.ID, &user.Email, &user.Name)
	return user, err
}
func (repo sqliteProjectRepository) AddMember(ctx context.Context, projectID, userID int64, role string) error {
	result, err := repo.db.ExecContext(ctx, "INSERT INTO project_members(project_id,user_id,role) VALUES(?,?,?) ON CONFLICT(project_id,user_id) DO NOTHING", projectID, userID, role)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errMemberAlreadyAdded
	}
	return nil
}

func (repo sqliteProjectRepository) ListMembers(ctx context.Context, projectID int64) ([]projectMember, error) {
	rows, err := repo.db.QueryContext(ctx, "SELECT u.id,u.email,u.name,m.role,m.created_at FROM project_members m JOIN users u ON u.id=m.user_id WHERE m.project_id=? ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END,u.id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]projectMember, 0)
	for rows.Next() {
		var member projectMember
		if err := rows.Scan(&member.User.ID, &member.User.Email, &member.User.Name, &member.Role, &member.JoinedAt); err != nil {
			return nil, err
		}
		items = append(items, member)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (repo sqliteProjectRepository) RemoveMember(ctx context.Context, projectID, userID int64) (bool, error) {
	var role string
	if err := repo.db.QueryRowContext(ctx, "SELECT role FROM project_members WHERE project_id=? AND user_id=?", projectID, userID).Scan(&role); err != nil {
		return false, err
	}
	if role == "owner" {
		return false, errProjectOwnerProtected
	}
	result, err := repo.db.ExecContext(ctx, "DELETE FROM project_members WHERE project_id=? AND user_id=?", projectID, userID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

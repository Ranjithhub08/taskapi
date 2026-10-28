// Package store talks to SQLite. Nothing else in the app touches SQL.
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers itself as "sqlite"

	"taskapi/models"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	title       TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'todo',
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);`

const columns = "id, title, description, status, created_at, updated_at"

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// New opens the database and creates the table if needed.
// Use ":memory:" for tests, or a "file:..." DSN for a real file.
func New(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time. One connection avoids "database is
	// locked" errors and also keeps ":memory:" databases consistent.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database is reachable (used by /health).
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanTask(sc scanner) (models.Task, error) {
	var t models.Task
	var created, updated string
	err := sc.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return t, ErrNotFound
		}
		return t, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339, created)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return t, nil
}

// Create inserts a task and returns it with its new ID.
func (s *Store) Create(ctx context.Context, in models.TaskInput) (models.Task, error) {
	ts := now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (title, description, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		in.Title, in.Description, in.Status, ts, ts)
	if err != nil {
		return models.Task{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return models.Task{}, err
	}
	return s.Get(ctx, id)
}

// Get returns one task or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (models.Task, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM tasks WHERE id = ?", id)
	return scanTask(row)
}

// List returns all tasks, newest first. If status is not empty, it filters by it.
func (s *Store) List(ctx context.Context, status string) ([]models.Task, error) {
	query := "SELECT " + columns + " FROM tasks"
	args := []any{}
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY id DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []models.Task{} // empty slice (not nil) so JSON shows [] instead of null
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// Update replaces the editable fields of a task.
func (s *Store) Update(ctx context.Context, id int64, in models.TaskInput) (models.Task, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET title = ?, description = ?, status = ?, updated_at = ? WHERE id = ?`,
		in.Title, in.Description, in.Status, now(), id)
	if err != nil {
		return models.Task{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.Task{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

// SetStatus changes only the status of a task.
func (s *Store) SetStatus(ctx context.Context, id int64, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`, status, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a task.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

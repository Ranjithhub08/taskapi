// Package models holds the data shapes shared by the whole app.
package models

import (
	"errors"
	"strings"
	"time"
)

// Allowed values for Task.Status.
const (
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
)

var validStatuses = map[string]bool{
	StatusTodo:       true,
	StatusInProgress: true,
	StatusDone:       true,
}

// Task is what we store in the database and send back as JSON.
type Task struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TaskInput is what the client sends when creating or updating a task.
type TaskInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// Validate cleans the input and returns an error if it is not acceptable.
// It uses a pointer receiver so it can trim spaces and fill defaults in place.
func (in *TaskInput) Validate() error {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)

	if in.Title == "" {
		return errors.New("title is required")
	}
	if len(in.Title) > 200 {
		return errors.New("title must be 200 characters or fewer")
	}
	if in.Status == "" {
		in.Status = StatusTodo
	}
	if !validStatuses[in.Status] {
		return errors.New("status must be one of: todo, in_progress, done")
	}
	return nil
}

// Package handlers contains the HTTP layer: routes, JSON in/out, status codes.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"taskapi/llm"
	"taskapi/models"
	"taskapi/store"
)

// API holds the dependencies every handler needs.
type API struct {
	Store *store.Store
	LLM   *llm.Client
}

// Routes registers all endpoints and wraps them with middleware.
// Go 1.22+ lets us put the HTTP method and a {param} in the pattern.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", a.health)

	mux.HandleFunc("POST /tasks", a.createTask)
	mux.HandleFunc("GET /tasks", a.listTasks)
	mux.HandleFunc("GET /tasks/{id}", a.getTask)
	mux.HandleFunc("PUT /tasks/{id}", a.updateTask)
	mux.HandleFunc("DELETE /tasks/{id}", a.deleteTask)

	mux.HandleFunc("POST /tasks/process", a.processTasks) // goroutine worker pool demo
	mux.HandleFunc("POST /ai/chat", a.aiChat)             // calls the LLM

	return recoverMiddleware(logMiddleware(mux))
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON reads the request body into dst. It rejects bodies over 1 MB and
// unknown fields, so typos in field names produce a clear 400 instead of
// being silently ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func parseID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

// storeError maps a store error to the right HTTP status.
func storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	log.Printf("store error: %v", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

// readTaskInput decodes and validates a task body. It writes the error
// response itself and returns false if the input is bad.
func readTaskInput(w http.ResponseWriter, r *http.Request) (models.TaskInput, bool) {
	var in models.TaskInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return in, false
	}
	if err := in.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return in, false
	}
	return in, true
}

// ---------- handlers ----------

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"llm_enabled": a.LLM.Enabled(),
	})
}

func (a *API) createTask(w http.ResponseWriter, r *http.Request) {
	in, ok := readTaskInput(w, r)
	if !ok {
		return
	}
	task, err := a.Store.Create(r.Context(), in)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (a *API) listTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" {
		// Reuse the same validation rules for the filter value.
		check := models.TaskInput{Title: "x", Status: status}
		if err := check.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	tasks, err := a.Store.List(r.Context(), status)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (a *API) getTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a number")
		return
	}
	task, err := a.Store.Get(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (a *API) updateTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a number")
		return
	}
	in, ok := readTaskInput(w, r)
	if !ok {
		return
	}
	task, err := a.Store.Update(r.Context(), id, in)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (a *API) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a number")
		return
	}
	if err := a.Store.Delete(r.Context(), id); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- goroutine example ----------

const (
	maxProcessIDs = 100
	workerCount   = 4
)

type processRequest struct {
	IDs []int64 `json:"ids"`
}

type processResult struct {
	ID    int64  `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// processTasks marks tasks as done using a pool of worker goroutines.
// If no ids are sent, it processes every task whose status is "todo".
//
// Pattern: a channel of job indexes, N workers reading from it, a WaitGroup
// to wait for all of them. Each worker writes only to its own slot of the
// results slice, so no mutex is needed and `go test -race` stays clean.
func (a *API) processTasks(w http.ResponseWriter, r *http.Request) {
	var req processRequest
	if err := decodeJSON(w, r, &req); err != nil && !errors.Is(err, io.EOF) { // io.EOF = empty body, which is allowed
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	ids := req.IDs
	if len(ids) == 0 {
		todo, err := a.Store.List(r.Context(), models.StatusTodo)
		if err != nil {
			storeError(w, err)
			return
		}
		for _, t := range todo {
			ids = append(ids, t.ID)
		}
	}
	if len(ids) > maxProcessIDs {
		writeError(w, http.StatusBadRequest, "too many ids (max 100)")
		return
	}

	results := make([]processResult, len(ids))
	jobs := make(chan int)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				results[idx] = a.processOne(r.Context(), ids[idx])
			}
		}()
	}
	for idx := range ids {
		jobs <- idx
	}
	close(jobs) // tells the workers "no more jobs", so their loops end
	wg.Wait()

	done := 0
	for _, res := range results {
		if res.OK {
			done++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"processed": done,
		"total":     len(ids),
		"results":   results,
	})
}

// processOne simulates 50 ms of work, then marks the task done.
func (a *API) processOne(ctx context.Context, id int64) processResult {
	select {
	case <-time.After(50 * time.Millisecond):
	case <-ctx.Done():
		return processResult{ID: id, Error: "cancelled"}
	}
	if err := a.Store.SetStatus(ctx, id, models.StatusDone); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return processResult{ID: id, Error: "task not found"}
		}
		return processResult{ID: id, Error: "database error"}
	}
	return processResult{ID: id, OK: true}
}

// ---------- LLM endpoint ----------

type chatRequest struct {
	Prompt string `json:"prompt"`
}

func (a *API) aiChat(w http.ResponseWriter, r *http.Request) {
	if !a.LLM.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "LLM not configured: set LLM_API_KEY")
		return
	}
	var req chatRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	reply, err := a.LLM.Chat(ctx, req.Prompt)
	if err != nil {
		log.Printf("llm error: %v", err)
		writeError(w, http.StatusBadGateway, "the AI provider returned an error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

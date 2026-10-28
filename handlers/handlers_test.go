package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"taskapi/llm"
	"taskapi/models"
	"taskapi/store"
)

// newTestServer starts the API on a throwaway in-memory database.
func newTestServer(t *testing.T, client *llm.Client) *httptest.Server {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	api := &API{Store: st, LLM: client}
	srv := httptest.NewServer(api.Routes())
	t.Cleanup(srv.Close)
	return srv
}

// do sends a request with an optional JSON body and returns the response.
func do(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func wantStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d", resp.StatusCode, want)
	}
}

func TestCRUD(t *testing.T) {
	srv := newTestServer(t, &llm.Client{})

	// create
	resp := do(t, "POST", srv.URL+"/tasks", map[string]string{"title": "  write tests ", "description": "important"})
	wantStatus(t, resp, http.StatusCreated)
	created := decode[models.Task](t, resp)
	if created.ID == 0 || created.Title != "write tests" || created.Status != "todo" {
		t.Fatalf("unexpected task: %+v", created)
	}

	// get
	resp = do(t, "GET", srv.URL+"/tasks/1", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := decode[models.Task](t, resp); got.ID != created.ID {
		t.Fatalf("got id %d, want %d", got.ID, created.ID)
	}

	// update
	resp = do(t, "PUT", srv.URL+"/tasks/1", map[string]string{"title": "write more tests", "status": "in_progress"})
	wantStatus(t, resp, http.StatusOK)
	if got := decode[models.Task](t, resp); got.Status != "in_progress" || got.Title != "write more tests" {
		t.Fatalf("update not applied: %+v", got)
	}

	// list with filter
	resp = do(t, "GET", srv.URL+"/tasks?status=in_progress", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := decode[[]models.Task](t, resp); len(got) != 1 {
		t.Fatalf("filtered list has %d items, want 1", len(got))
	}
	resp = do(t, "GET", srv.URL+"/tasks?status=done", nil)
	if got := decode[[]models.Task](t, resp); len(got) != 0 {
		t.Fatalf("done list has %d items, want 0", len(got))
	}

	// delete, then it is gone
	wantStatus(t, do(t, "DELETE", srv.URL+"/tasks/1", nil), http.StatusNoContent)
	wantStatus(t, do(t, "GET", srv.URL+"/tasks/1", nil), http.StatusNotFound)
	wantStatus(t, do(t, "DELETE", srv.URL+"/tasks/1", nil), http.StatusNotFound)
}

func TestValidationAndBadInput(t *testing.T) {
	srv := newTestServer(t, &llm.Client{})

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{"missing title", "POST", "/tasks", map[string]string{"description": "x"}, http.StatusBadRequest},
		{"bad status", "POST", "/tasks", map[string]string{"title": "a", "status": "nope"}, http.StatusBadRequest},
		{"unknown field", "POST", "/tasks", map[string]string{"title": "a", "colour": "red"}, http.StatusBadRequest},
		{"non-numeric id", "GET", "/tasks/abc", nil, http.StatusBadRequest},
		{"bad status filter", "GET", "/tasks?status=zzz", nil, http.StatusBadRequest},
		{"update missing task", "PUT", "/tasks/999", map[string]string{"title": "a"}, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantStatus(t, do(t, tc.method, srv.URL+tc.path, tc.body), tc.want)
		})
	}
}

func TestProcessUsesWorkerPool(t *testing.T) {
	srv := newTestServer(t, &llm.Client{})

	for i := 0; i < 10; i++ {
		wantStatus(t, do(t, "POST", srv.URL+"/tasks", map[string]string{"title": "job"}), http.StatusCreated)
	}

	// empty body = process every todo task
	resp := do(t, "POST", srv.URL+"/tasks/process", nil)
	wantStatus(t, resp, http.StatusOK)
	out := decode[struct {
		Processed int `json:"processed"`
		Total     int `json:"total"`
	}](t, resp)
	if out.Processed != 10 || out.Total != 10 {
		t.Fatalf("processed %d of %d, want 10 of 10", out.Processed, out.Total)
	}

	resp = do(t, "GET", srv.URL+"/tasks?status=todo", nil)
	if got := decode[[]models.Task](t, resp); len(got) != 0 {
		t.Fatalf("%d tasks still todo", len(got))
	}

	// unknown ids are reported, not fatal
	resp = do(t, "POST", srv.URL+"/tasks/process", map[string]any{"ids": []int64{9999}})
	wantStatus(t, resp, http.StatusOK)
	if got := decode[struct {
		Processed int `json:"processed"`
	}](t, resp); got.Processed != 0 {
		t.Fatalf("processed = %d, want 0", got.Processed)
	}
}

func TestAIChatNotConfigured(t *testing.T) {
	srv := newTestServer(t, &llm.Client{}) // no API key
	resp := do(t, "POST", srv.URL+"/ai/chat", map[string]string{"prompt": "hi"})
	wantStatus(t, resp, http.StatusServiceUnavailable)
}

func TestAIChatWithFakeProvider(t *testing.T) {
	// A fake OpenAI-compatible server, so the test needs no real key or internet.
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello from fake"}}]}`))
	}))
	defer fake.Close()

	client := &llm.Client{BaseURL: fake.URL, APIKey: "test-key", Model: "m", HTTP: fake.Client()}
	srv := newTestServer(t, client)

	resp := do(t, "POST", srv.URL+"/ai/chat", map[string]string{"prompt": "hi"})
	wantStatus(t, resp, http.StatusOK)
	if got := decode[map[string]string](t, resp); !strings.Contains(got["reply"], "hello from fake") {
		t.Fatalf("unexpected reply: %v", got)
	}

	// wrong key makes the provider fail, and we report a 502
	bad := &llm.Client{BaseURL: fake.URL, APIKey: "wrong", Model: "m", HTTP: fake.Client()}
	srv2 := newTestServer(t, bad)
	wantStatus(t, do(t, "POST", srv2.URL+"/ai/chat", map[string]string{"prompt": "hi"}), http.StatusBadGateway)
}

func TestHealth(t *testing.T) {
	srv := newTestServer(t, &llm.Client{})
	wantStatus(t, do(t, "GET", srv.URL+"/health", nil), http.StatusOK)
}

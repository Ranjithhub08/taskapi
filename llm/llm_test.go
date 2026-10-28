package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeProvider(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL + "/", APIKey: "k", Model: "m", HTTP: srv.Client()}
}

func TestChatSuccess(t *testing.T) {
	c := fakeProvider(t, 200, `{"choices":[{"message":{"role":"assistant","content":"pong"}}]}`)
	got, err := c.Chat(context.Background(), "ping")
	if err != nil || got != "pong" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestChatProviderError(t *testing.T) {
	c := fakeProvider(t, 500, `oops`)
	if _, err := c.Chat(context.Background(), "ping"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("want a 500 error, got %v", err)
	}
}

func TestChatNoChoices(t *testing.T) {
	c := fakeProvider(t, 200, `{"choices":[]}`)
	if _, err := c.Chat(context.Background(), "ping"); err == nil {
		t.Fatal("want an error for empty choices")
	}
}

func TestChatWithoutKey(t *testing.T) {
	c := &Client{}
	if c.Enabled() {
		t.Fatal("client without key must not be enabled")
	}
	if _, err := c.Chat(context.Background(), "ping"); err == nil {
		t.Fatal("want an error when no key is set")
	}
}

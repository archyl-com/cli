package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCreds struct{ refreshed bool }

func (f *fakeCreds) Apply(_ context.Context, req *http.Request) error {
	token := "old"
	if f.refreshed {
		token = "new"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (f *fakeCreds) Invalidate(context.Context) (bool, error) {
	f.refreshed = true
	return true, nil
}

func TestChatStreams(
	t *testing.T,
) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":true,"message":"expired"}`))
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{"The Harness ", "opens **work sessions**."} {
			payload, _ := json.Marshal(map[string]string{"content": chunk})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		}
		_, _ = fmt.Fprint(w, ": keep-alive\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(server.URL, "org", &fakeCreds{})
	var got strings.Builder
	err := client.Chat(context.Background(), "p1", []ChatMessage{{Role: "user", Content: "what?"}}, func(s string) { got.WriteString(s) })
	require.NoError(t, err)
	assert.Equal(t, "The Harness opens **work sessions**.", got.String())
	assert.Equal(t, "p1", body["currentProjectId"], "a 401 is retried once with refreshed credentials")
}

func TestChatError(
	t *testing.T,
) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":true,"message":"Chat is not available on your current plan."}`))
	}))
	defer server.Close()

	err := NewClient(server.URL, "", nil).Chat(context.Background(), "", nil, func(string) {})
	assert.True(t, IsStatus(err, http.StatusForbidden))
	assert.Contains(t, err.Error(), "current plan")
}

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnsupportedRoute(
	t *testing.T,
) {
	testCases := []struct {
		name            string
		status          int
		body            string
		wantUnsupported bool
		wantMessage     string
	}{
		{
			name:            "router 404 from Archyl's error handler",
			status:          http.StatusNotFound,
			body:            `{"error":true,"message":"Not Found"}`,
			wantUnsupported: true,
			wantMessage:     ErrServerTooOld.Error(),
		},
		{
			name:            "Fiber default 404",
			status:          http.StatusNotFound,
			body:            `Cannot POST /api/v1/projects/p/dsl/diff`,
			wantUnsupported: true,
			wantMessage:     ErrServerTooOld.Error(),
		},
		{
			name:        "missing resource",
			status:      http.StatusNotFound,
			body:        `{"error":"Project not found"}`,
			wantMessage: "Project not found",
		},
		{
			name:        "other error",
			status:      http.StatusForbidden,
			body:        `{"error":"Not Found"}`,
			wantMessage: "Not Found",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()

			err := NewClient(server.URL, "", nil).do(context.Background(), http.MethodPost, "/x", nil, nil, nil)

			var apiErr *Error
			assert.ErrorAs(t, err, &apiErr)
			assert.Equal(t, testCase.wantUnsupported, IsUnsupported(err))
			assert.Equal(t, testCase.wantMessage, apiErr.Message)
		})
	}
}

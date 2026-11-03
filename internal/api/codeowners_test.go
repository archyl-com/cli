package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwnershipMap(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		status      int
		body        string
		want        []OwnedElement
		unsupported bool
	}{
		{
			name:   "elements with people and teams",
			status: http.StatusOK,
			body: `{"elements":[{"id":"c1","name":"Orders API","type":"container","projectId":"p1",` +
				`"owners":[{"userId":"u","email":"ada@example.com","firstName":"Ada","lastName":"Lovelace"}],` +
				`"teamOwners":[{"teamId":"t","name":"Checkout","slug":"checkout"}]}],"coverage":{"total":1,"owned":1}}`,
			want: []OwnedElement{{
				ID: "c1", Name: "Orders API", Type: "container", ProjectID: "p1",
				Owners:     []Owner{{Email: "ada@example.com", FirstName: "Ada", LastName: "Lovelace"}},
				TeamOwners: []TeamOwner{{Name: "Checkout", Slug: "checkout"}},
			}},
		},
		{
			name:        "a server without the route",
			status:      http.StatusNotFound,
			body:        `{"error":true,"message":"Not Found"}`,
			unsupported: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/global/ownership-map", r.URL.Path)
				assert.Equal(t, "org", r.Header.Get("X-Organization-ID"))
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()

			got, err := NewClient(server.URL, "org", nil).OwnershipMap(context.Background())
			if testCase.unsupported {
				assert.True(t, IsUnsupported(err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

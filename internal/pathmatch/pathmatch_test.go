package pathmatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatches(
	t *testing.T,
) {
	testCases := []struct {
		segment string
		name    string
		want    bool
	}{
		{segment: "orders", name: "Orders Service", want: true},
		{segment: "billing-api", name: "Billing API", want: true},
		{segment: "payments", name: "Payments API", want: true},
		{segment: "wf-worker-inference", name: "wf-worker-inference", want: true},
		{segment: "api", name: "Shop API"},
		{segment: "db", name: "DB", want: true},
		{segment: "ui", name: "UI", want: true},
		{segment: "billing-api", name: "Billing", want: true},
		{segment: "orders-service", name: "Orders", want: true},
		{segment: "x", name: "X"},
		{segment: "order", name: "Orders"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.segment+"/"+testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Matches(testCase.segment, testCase.name))
		})
	}
}

func TestFolder(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		files []string
		want  string
	}{
		{
			name:  "transcoding-api",
			files: []string{"activity/transcoding/activity.go", "api/transcoding/cmd/main.go"},
			want:  "api/transcoding",
		},
		{
			name:  "Orders Service",
			files: []string{"internal/orders/x.go", "services/orders-service/main.go"},
			want:  "services/orders-service",
		},
		{
			name:  "Orders",
			files: []string{"b/orders/x.go", "a/deep/orders/y.go", "a/orders/z.go"},
			want:  "a/orders",
		},
		{
			name:  "Payments",
			files: []string{"payments.go", "src/main.go"},
		},
		{
			name:  "DB",
			files: []string{"db/x.go"},
			want:  "db",
		},
		{
			name:  "Billing",
			files: []string{"services/billing-api/main.go"},
			want:  "services/billing-api",
		},
		{
			name:  "X",
			files: []string{"x/main.go"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Folder(testCase.name, testCase.files))
		})
	}
}

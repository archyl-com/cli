package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagramInside(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{name: "a system's containers", args: []string{"diagram", "Shop", "--inside", "--format", "mermaid"}, want: "Orders API"},
		{name: "a leaf has nothing inside", args: []string{"diagram", "Storefront", "--inside", "--format", "mermaid"}, wantErr: "holds no elements to draw inside it"},
		{name: "centred by default", args: []string{"diagram", "Storefront", "--format", "mermaid"}, want: "Storefront"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out, err := runTourErr(t, testCase.args...)
			if testCase.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, out, testCase.want)
		})
	}
}

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOpenBrowserRefusesWhatIsNotAWebAddress(t *testing.T) {
	for _, target := range []string{"file:///System/Applications/Calculator.app", "-a Calculator", "javascript:alert(1)", "https://"} {
		assert.Error(t, OpenBrowser(target), target)
	}
}

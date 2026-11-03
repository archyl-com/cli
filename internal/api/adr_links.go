package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// adrLinkLevels is how the ADR link API numbers the C4 levels.
var adrLinkLevels = map[string]int{
	"system":       1,
	"container":    2,
	"component":    3,
	"code":         4,
	"code_element": 4,
}

// ADRLinkLevel is the number the ADR link API expects for a C4 level name
// ("system", "container", "component", "code"), and whether there is one.
func ADRLinkLevel(level string) (int, bool) {
	n, ok := adrLinkLevels[level]
	return n, ok
}

// LinkADR ties a decision to an element of the given C4 level.
func (c *Client) LinkADR(
	ctx context.Context,
	adrID string,
	elementID string,
	level string,
) error {
	n, ok := ADRLinkLevel(level)
	if !ok {
		return fmt.Errorf("cannot link a decision to a %q element", level)
	}
	body := map[string]any{"elementId": elementID, "elementType": n}
	return c.do(ctx, http.MethodPost, "/adrs/"+url.PathEscape(adrID)+"/links", nil, body, nil)
}

package httpapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"jobpilot/core/api/internal/store"
)

func TestCVReviewBlockerResponse(t *testing.T) {
	w := httptest.NewRecorder()
	respond(w, nil, fmt.Errorf("%w: PDF text layer: phone is missing", store.ErrCVReviewBlocked))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "phone") {
		t.Fatalf("review blocker response: %d %s", w.Code, w.Body.String())
	}
}

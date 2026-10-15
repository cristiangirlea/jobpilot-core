package httpapi

import (
	"net/http/httptest"
	"testing"
)

// A malformed query is refused before the store is touched, with a message
// that names the accepted values.
func TestNotificationsRefuseABadQuery(t *testing.T) {
	for _, query := range []string{"state=pending", "limit=0", "limit=201", "limit=many"} {
		w := httptest.NewRecorder()
		(&Server{}).listNotifications(w, httptest.NewRequest("GET", "/api/notifications?"+query, nil))
		if w.Code != 400 {
			t.Errorf("%s: want 400, got %d (%s)", query, w.Code, w.Body.String())
		}
	}
}

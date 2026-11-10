package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHistoryImportValidationBeforeStore(t *testing.T) {
	for _, body := range []string{`{}`, `{"record":{"external_key":"x"}}`, `{"mode":"send"}`, `{"unknown":"field"}`, `{} {}`} {
		w := httptest.NewRecorder()
		(&Server{}).importExternalHistory(w, httptest.NewRequest("POST", "/api/application-history/import", strings.NewReader(body)))
		if w.Code != 400 && w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, q := range []string{"limit=0", "limit=101", "limit=bad"} {
		w := httptest.NewRecorder()
		(&Server{}).listExternalHistory(w, httptest.NewRequest("GET", "/api/application-history?"+q, nil))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}

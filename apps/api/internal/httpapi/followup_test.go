package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuietApplicationSettingValidation(t *testing.T) {
	for _, days := range []string{"-1", "366"} {
		w := httptest.NewRecorder()
		(&Server{}).saveAccountSettings(w, httptest.NewRequest("PUT", "/api/account/settings", strings.NewReader(`{"quiet_application_days":`+days+`}`)))
		if w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

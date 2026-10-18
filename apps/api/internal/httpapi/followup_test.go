package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuietApplicationSettingValidation(t *testing.T) {
	for _, days := range []string{"-1", "366"} {
		w := httptest.NewRecorder()
		(&Server{}).saveDiscoverySettings(w, httptest.NewRequest("PUT", "/api/discovery/settings", strings.NewReader(`{"enabled":false,"interval_minutes":360,"auto_prepare":false,"min_score":75,"max_jobs_per_run":100,"quiet_application_days":`+days+`}`)))
		if w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

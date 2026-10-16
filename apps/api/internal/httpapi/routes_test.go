package httpapi

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// readOnlyPosts are the only non-GET routes an agent token with just the read
// scope may reach. Both are lookups: validate fetches a sample from a
// provider and detect parses a URL. Neither changes anything the user owns.
//
// Adding to this list widens what a read-only token can do, so it is a
// deliberate decision, not a default.
var readOnlyPosts = map[string]bool{
	"POST /api/discovery/validate": true,
	"POST /api/discovery/detect":   true,
}

// sessionOnly routes refuse agent tokens outright, each through the wrapper
// named here: token management, so that a leaked token cannot mint siblings or
// revoke itself to cover its tracks, approving an application, which is
// the user's consent to send it, and invites, which also need an admin.
var sessionOnly = map[string]string{
	"GET /api/invites":              "admin",
	"POST /api/invites":             "admin",
	"DELETE /api/invites/{id}":      "admin",
	"GET /api/tokens":               "session",
	"POST /api/tokens":              "session",
	"DELETE /api/tokens/{id}":       "session",
	"POST /api/outbox/{id}/approve": "consent",
	// Linking a Google identity adds a way into the account.
	"POST /api/auth/google/link": "session",
}

// adminOnly routes are for the accounts ADMIN_EMAILS names (#128): ap( for
// reading, aw( for changing, which also needs the write scope. Discovery is
// the admin's, and so is the configured AI provider's status. Moving a route
// off this list hands it to every member.
var adminOnly = map[string]string{
	"GET /api/ai/status":                 "ap",
	"GET /api/discovery/settings":        "ap",
	"PUT /api/discovery/settings":        "aw",
	"GET /api/discovery/sources":         "ap",
	"POST /api/discovery/sources":        "aw",
	"PUT /api/discovery/sources/{id}":    "aw",
	"POST /api/discovery/validate":       "ap",
	"DELETE /api/discovery/sources/{id}": "aw",
	"GET /api/discovery/runs":            "ap",
	"POST /api/discovery/run":            "aw",
	"POST /api/discovery/recommended":    "aw",
	"GET /api/discovery/suggestions":     "ap",
	"POST /api/discovery/detect":         "ap",
}

var routePattern = regexp.MustCompile(`m\.Handle\("([^"]+)",\s*(\w+)\(`)

// TestEveryMutatingRouteRequiresTheWriteScope reads the router itself, because
// the rule it checks lives in how each route is registered rather than in any
// value a request could observe. A route added with the wrong wrapper would
// let a read-only agent token change the user's data, and nothing else in the
// suite would notice.
func TestEveryMutatingRouteRequiresTheWriteScope(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	matches := routePattern.FindAllStringSubmatch(string(source), -1)
	if len(matches) < 40 {
		t.Fatalf("only found %d routes; the registration pattern probably changed, which would make this test vacuous", len(matches))
	}

	seenWrite, seenSession, seenAdmin := 0, 0, 0
	for _, m := range matches {
		route, wrapper := m[1], m[2]
		want, sessionRoute := sessionOnly[route]
		adminWant, adminRoute := adminOnly[route]
		switch {
		case adminRoute:
			if wrapper != adminWant {
				t.Errorf("%s is for admins through the %s( wrapper, not %s(", route, adminWant, wrapper)
			}
			if adminWant == "ap" && !(strings.HasPrefix(route, "GET ") || readOnlyPosts[route]) {
				t.Errorf("%s changes stored state, so aw( is expected", route)
			}
			seenAdmin++
		case sessionRoute:
			if wrapper != want {
				t.Errorf("%s must refuse agent tokens through the %s( wrapper, not %s(", route, want, wrapper)
			}
			seenSession++
		case strings.HasPrefix(route, "GET ") || readOnlyPosts[route]:
			if wrapper != "p" {
				t.Errorf("%s only reads, so the p( wrapper is expected, not %s(", route, wrapper)
			}
		default:
			if wrapper != "w" {
				t.Errorf("%s changes stored state, so it must use the w( wrapper for the write scope, not %s(", route, wrapper)
			}
			seenWrite++
		}
	}
	if seenWrite < 30 {
		t.Errorf("expected most routes to be write-scoped, got %d", seenWrite)
	}
	if seenAdmin != len(adminOnly) {
		t.Errorf("expected %d admin-only routes, found %d", len(adminOnly), seenAdmin)
	}
	if seenSession != len(sessionOnly) {
		t.Errorf("expected %d session-only routes, found %d", len(sessionOnly), seenSession)
	}
}

// TestUnauthenticatedRoutesAreTheExpectedOnes guards the other direction:
// everything except health, signing in, the registration mode and the two
// emailed links (verify, reset) goes through a wrapper.
func TestUnauthenticatedRoutesAreTheExpectedOnes(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	open := regexp.MustCompile(`m\.HandleFunc\("([^"]+)"`).FindAllStringSubmatch(string(source), -1)
	want := map[string]bool{
		"GET /api/health":         true,
		"POST /api/auth/register": true,
		"POST /api/auth/login":    true,
		// Sign-in with Google: the provider list, the redirect to Google and
		// back, and the single-use code the web app exchanges for its token.
		"GET /api/auth/providers":               true,
		"GET /api/auth/google/start":            true,
		"GET /api/auth/google/callback":         true,
		"POST /api/auth/google/exchange":        true,
		"GET /api/auth/registration":            true,
		"POST /api/auth/verify-email":           true,
		"POST /api/auth/password-reset":         true,
		"POST /api/auth/password-reset/confirm": true,
	}
	for _, m := range open {
		if !want[m[1]] {
			t.Errorf("%s is registered without authentication; every other route needs a wrapper", m[1])
		}
		delete(want, m[1])
	}
	for route := range want {
		t.Errorf("%s is no longer registered as an open route; update this test if that was intended", route)
	}
}

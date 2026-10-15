package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/store"
)

// authProviders tells the login page which sign-in buttons to show.
func (s *Server) authProviders(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"google": s.Google != nil})
}

func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) {
	if s.Google == nil {
		problem(w, 404, "Sign-in with Google is not enabled.")
		return
	}
	s.Google.Start(w, r)
}

// googleCallback ends where it began, in the browser: on the web app's login
// page with an error, or on its Google page with a code that, with the binding
// cookie set here, is exchanged for the session token (or, for a link flow,
// links the identity to the signed-in account). Neither a token nor the
// identity appears in a URL.
func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	if s.Google == nil {
		problem(w, 404, "Sign-in with Google is not enabled.")
		return
	}
	id, link, err := s.Google.Callback(w, r)
	if err != nil {
		slog.Warn("google sign-in refused", "error", err)
		s.toFrontend(w, r, "/login", url.Values{"error": {"google"}})
		return
	}
	var grant auth.Grant
	if link {
		grant.Identity = id
	} else {
		u, err := s.Store.SignInWithIdentity(r.Context(), id.Provider, id.Subject, id.Email, s.googleMayCreate(r.Context()))
		switch {
		case errors.Is(err, store.ErrNoAccount):
			s.toFrontend(w, r, "/login", url.Values{"error": {"google_no_account"}})
			return
		case errors.Is(err, store.ErrLinkRequired):
			s.toFrontend(w, r, "/login", url.Values{"error": {"google_link"}})
			return
		case err != nil:
			slog.Error("google sign-in failed", "error", err)
			s.toFrontend(w, r, "/login", url.Values{"error": {"google"}})
			return
		}
		if grant.Token, err = auth.Token(u.ID, u.Email, s.Secret, s.TokenTTL); err != nil {
			slog.Error("google sign-in failed", "error", err)
			s.toFrontend(w, r, "/login", url.Values{"error": {"google"}})
			return
		}
	}
	code, binding := s.handoff.Issue(grant)
	s.Google.Bind(w, binding)
	q := url.Values{"code": {code}}
	if link {
		q.Set("link", "1")
	}
	s.toFrontend(w, r, "/login/google", q)
}

// googleMayCreate reports whether Google may create an account here: only
// with GOOGLE_ALLOW_SIGNUP on and sign-up open to anyone. The callback has no
// invite to show, so with invite-only or closed registration an invited
// person signs up with the code and links Google afterwards.
func (s *Server) googleMayCreate(ctx context.Context) bool {
	return s.GoogleSignup && s.checkSignUp(ctx, "") == nil
}

func (s *Server) toFrontend(w http.ResponseWriter, r *http.Request, path string, q url.Values) {
	http.Redirect(w, r, s.FrontendURL+path+"?"+q.Encode(), http.StatusFound)
}

// redeem reads the handoff code from the body and returns its grant, only
// for the browser holding the binding cookie.
func (s *Server) redeem(w http.ResponseWriter, r *http.Request) (auth.Grant, bool) {
	var x struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &x) {
		return auth.Grant{}, false
	}
	g, ok := s.handoff.Redeem(x.Code, auth.Binding(w, r))
	if !ok {
		problem(w, 401, "This sign-in link has expired or belongs to another browser. Sign in again.")
	}
	return g, ok
}

func (s *Server) googleExchange(w http.ResponseWriter, r *http.Request) {
	g, ok := s.redeem(w, r)
	if !ok {
		return
	}
	if g.Token == "" {
		problem(w, 401, "This sign-in link has expired or belongs to another browser. Sign in again.")
		return
	}
	write(w, 200, map[string]any{"token": g.Token})
}

// googleLink links a Google identity to the signed-in account. It is the only
// way an existing account gets one: being signed in is what proves the
// account is the caller's, which a matching email does not.
func (s *Server) googleLink(w http.ResponseWriter, r *http.Request) {
	g, ok := s.redeem(w, r)
	if !ok {
		return
	}
	if g.Identity.Subject == "" {
		problem(w, 401, "This sign-in link has expired or belongs to another browser. Sign in again.")
		return
	}
	err := s.Store.LinkIdentity(r.Context(), auth.UserID(r.Context()), g.Identity.Provider, g.Identity.Subject, g.Identity.Email)
	if errors.Is(err, store.ErrIdentityTaken) {
		problem(w, 409, "This Google account is already linked to another JobPilot account.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 200, map[string]any{"linked": "google", "email": g.Identity.Email})
}

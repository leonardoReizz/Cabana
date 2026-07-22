package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/leonardoreis/cabana/internal/domain"
	"github.com/leonardoreis/cabana/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const sessionTTL = 7 * 24 * time.Hour

type ctxKey string

const ctxKeyUser ctxKey = "cabana.user"

// authed wraps a handler so it only runs for a valid session, and puts the
// resolved user on the request context.
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		user, err := s.store.UserBySessionToken(r.Context(), token)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or expired session")
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal", "could not verify session")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKeyUser, user)))
	}
}

func userFrom(ctx context.Context) domain.User {
	u, _ := ctx.Value(ctxKeyUser).(domain.User)
	return u
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string      `json:"token"`
	ExpiresAt time.Time   `json:"expires_at"`
	User      domain.User `json:"user"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	user, err := s.store.UserByUsername(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Spend the same work as a real comparison so a missing username is
			// not distinguishable from a wrong password by timing.
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
			writeErr(w, http.StatusUnauthorized, "invalid_credentials", "username or password is incorrect")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not look up user")
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "username or password is incorrect")
		return
	}

	token, err := newToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not issue session")
		return
	}
	expiresAt, err := s.store.CreateSession(r.Context(), user.ID, token, sessionTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not persist session")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{Token: token, ExpiresAt: expiresAt, User: user})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSession(r.Context(), bearerToken(r)); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not end session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r.Context()))
}

// dummyHash is a valid bcrypt hash of a value nobody can log in with. It exists
// only to give the unknown-username path the same cost as the real one.
var dummyHash = []byte("$2a$12$eImiTXuWVxfM37uY4JANjQ.LZuLPZLZLZLZLZLZLZLZLZLZLZLZLZ")

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashPassword is used at bootstrap to seed the admin user.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

// ConstantTimeEquals is kept for future API-token comparison (v0.5 CLI tokens),
// where the value is compared directly rather than through bcrypt.
func ConstantTimeEquals(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

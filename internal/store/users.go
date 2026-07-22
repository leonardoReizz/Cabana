package store

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/leonardoreis/cabana/internal/domain"
)

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (domain.User, error) {
	u := domain.User{ID: domain.NewID(), Username: username, PasswordHash: passwordHash}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, $3) RETURNING created_at`,
		u.ID, u.Username, u.PasswordHash).Scan(&u.CreatedAt)
	return u, mapErr(err)
}

func (s *Store) UserByUsername(ctx context.Context, username string) (domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, password_hash, created_at FROM users WHERE username = $1`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	return u, mapErr(err)
}

// HashToken is the one-way transform applied before a session token touches the
// database. Exported so the API layer hashes and the store never sees a raw token.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (s *Store) CreateSession(ctx context.Context, userID, token string, ttl time.Duration) (time.Time, error) {
	expiresAt := time.Now().Add(ttl)
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		HashToken(token), userID, expiresAt)
	return expiresAt, mapErr(err)
}

// UserBySessionToken resolves a bearer token to its user, treating an expired
// session as absent.
func (s *Store) UserBySessionToken(ctx context.Context, token string) (domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.username, u.password_hash, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, HashToken(token)).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	return u, mapErr(err)
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, HashToken(token))
	return mapErr(err)
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

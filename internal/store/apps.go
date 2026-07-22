package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/leonardoreis/cabana/internal/domain"
)

func (s *Store) CreateApp(ctx context.Context, a domain.App) (domain.App, error) {
	a.ID = domain.NewID()
	err := s.pool.QueryRow(ctx, `
		INSERT INTO apps (id, name, repo_url, git_ref, status, hostname,
			replicas, health_type, health_path, health_initial_delay, health_period)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING created_at, updated_at`,
		a.ID, a.Name, a.RepoURL, a.GitRef, a.Status, a.Hostname,
		a.Replicas, a.Health.Type, a.Health.Path, a.Health.InitialDelaySeconds,
		a.Health.PeriodSeconds).
		Scan(&a.CreatedAt, &a.UpdatedAt)
	return a, mapErr(err)
}

func (s *Store) ListApps(ctx context.Context) ([]domain.App, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, repo_url, git_ref, status, hostname, replicas, health_type, health_path, health_initial_delay, health_period, created_at, updated_at
		FROM apps ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil zero-length slice so the API serialises [] rather than null.
	apps := []domain.App{}
	for rows.Next() {
		var a domain.App
		if err := rows.Scan(&a.ID, &a.Name, &a.RepoURL, &a.GitRef, &a.Status, &a.Hostname,
			&a.Replicas, &a.Health.Type, &a.Health.Path, &a.Health.InitialDelaySeconds,
			&a.Health.PeriodSeconds, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		apps = append(apps, a)
	}
	return apps, rows.Err()
}

func (s *Store) AppByID(ctx context.Context, id string) (domain.App, error) {
	var a domain.App
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, repo_url, git_ref, status, hostname, replicas, health_type, health_path, health_initial_delay, health_period, created_at, updated_at
		FROM apps WHERE id = $1`, id).
		Scan(&a.ID, &a.Name, &a.RepoURL, &a.GitRef, &a.Status, &a.Hostname,
			&a.Replicas, &a.Health.Type, &a.Health.Path, &a.Health.InitialDelaySeconds,
			&a.Health.PeriodSeconds, &a.CreatedAt, &a.UpdatedAt)
	return a, mapErr(err)
}

func (s *Store) SetAppStatus(ctx context.Context, id string, status domain.AppStatus) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE apps SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteApp(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AppEnv(ctx context.Context, appID string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, value FROM app_env WHERE app_id = $1 ORDER BY key`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	env := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		env[k] = v
	}
	return env, rows.Err()
}

// ReplaceAppEnv makes the stored env exactly match the map passed in. It is a
// full replace rather than a merge so the dashboard can own the whole set and
// deletions need no separate endpoint.
func (s *Store) ReplaceAppEnv(ctx context.Context, appID string, env map[string]string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM app_env WHERE app_id = $1`, appID); err != nil {
		return err
	}
	if len(env) > 0 {
		batch := &pgx.Batch{}
		for k, v := range env {
			batch.Queue(`INSERT INTO app_env (app_id, key, value) VALUES ($1, $2, $3)`, appID, k, v)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetAppHostname records where an app actually ended up being served, which the
// deployer only knows once a fallback (sslip.io) has been resolved.
func (s *Store) SetAppHostname(ctx context.Context, id, hostname string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE apps SET hostname = $2, updated_at = now() WHERE id = $1`, id, hostname)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAppSettings changes the knobs that only take effect on the next deploy.
// Kept separate from the app's identity (name, repo) which is immutable.
func (s *Store) UpdateAppSettings(ctx context.Context, id string, replicas int32, h domain.Health) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE apps SET replicas = $2, health_type = $3, health_path = $4,
			health_initial_delay = $5, health_period = $6, updated_at = now()
		WHERE id = $1`,
		id, replicas, h.Type, h.Path, h.InitialDelaySeconds, h.PeriodSeconds)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AppBuildArgs(ctx context.Context, appID string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, value FROM app_build_args WHERE app_id = $1 ORDER BY key`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	args := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		args[k] = v
	}
	return args, rows.Err()
}

// ReplaceAppBuildArgs makes the stored build args exactly match the map given,
// mirroring ReplaceAppEnv so both editors behave the same way.
func (s *Store) ReplaceAppBuildArgs(ctx context.Context, appID string, args map[string]string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM app_build_args WHERE app_id = $1`, appID); err != nil {
		return err
	}
	if len(args) > 0 {
		batch := &pgx.Batch{}
		for k, v := range args {
			batch.Queue(
				`INSERT INTO app_build_args (app_id, key, value) VALUES ($1, $2, $3)`,
				appID, k, v)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

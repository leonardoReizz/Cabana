package store

import (
	"context"
	"time"

	"github.com/leonardoreis/cabana/internal/domain"
)

// CreateDeploy records a new deploy. rolledBackFrom is nil for a normal build
// and names the restored deploy for a rollback.
func (s *Store) CreateDeploy(ctx context.Context, appID, gitRef, image string, rolledBackFrom *string) (domain.Deploy, error) {
	d := domain.Deploy{
		ID:             domain.NewID(),
		AppID:          appID,
		Status:         domain.DeployStatusQueued,
		GitRef:         gitRef,
		Image:          image,
		RolledBackFrom: rolledBackFrom,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO deploys (id, app_id, status, git_ref, image, rolled_back_from)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`,
		d.ID, d.AppID, d.Status, d.GitRef, d.Image, d.RolledBackFrom).Scan(&d.CreatedAt)
	return d, mapErr(err)
}

// SetDeployImage records what a build produced, once it is known.
func (s *Store) SetDeployImage(ctx context.Context, id, image string) error {
	_, err := s.pool.Exec(ctx, `UPDATE deploys SET image = $2 WHERE id = $1`, id, image)
	return mapErr(err)
}

func (s *Store) ListDeploys(ctx context.Context, appID string, limit int) ([]domain.Deploy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, app_id, status, git_ref, commit_sha, message, image, rolled_back_from, created_at, finished_at
		FROM deploys WHERE app_id = $1 ORDER BY created_at DESC LIMIT $2`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	deploys := []domain.Deploy{}
	for rows.Next() {
		var d domain.Deploy
		if err := rows.Scan(&d.ID, &d.AppID, &d.Status, &d.GitRef, &d.CommitSHA, &d.Message,
			&d.Image, &d.RolledBackFrom, &d.CreatedAt, &d.FinishedAt); err != nil {
			return nil, err
		}
		deploys = append(deploys, d)
	}
	return deploys, rows.Err()
}

func (s *Store) DeployByID(ctx context.Context, id string) (domain.Deploy, error) {
	var d domain.Deploy
	err := s.pool.QueryRow(ctx, `
		SELECT id, app_id, status, git_ref, commit_sha, message, image, rolled_back_from, created_at, finished_at
		FROM deploys WHERE id = $1`, id).
		Scan(&d.ID, &d.AppID, &d.Status, &d.GitRef, &d.CommitSHA, &d.Message,
			&d.Image, &d.RolledBackFrom, &d.CreatedAt, &d.FinishedAt)
	return d, mapErr(err)
}

// UpdateDeployStatus stamps finished_at automatically once the deploy reaches a
// terminal state, so callers never have to remember to.
func (s *Store) UpdateDeployStatus(ctx context.Context, id string, status domain.DeployStatus, message string) error {
	var finishedAt *time.Time
	if status == domain.DeployStatusSucceeded || status == domain.DeployStatusFailed {
		now := time.Now()
		finishedAt = &now
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE deploys SET status = $2, message = $3, finished_at = $4 WHERE id = $1`,
		id, status, message, finishedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MaxDeployLogBytes caps stored build output. Generous enough for a verbose
// multi-stage build, bounded enough that a runaway build cannot fill the disk.
const MaxDeployLogBytes = 1 << 20 // 1 MiB

// SaveDeployLog stores a deploy's build output, keeping the tail when it
// exceeds the cap — the end of a failed build is the part that explains it.
func (s *Store) SaveDeployLog(ctx context.Context, deployID, content string) error {
	truncated := false
	if len(content) > MaxDeployLogBytes {
		content = content[len(content)-MaxDeployLogBytes:]
		truncated = true
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO deploy_logs (deploy_id, content, truncated)
		VALUES ($1, $2, $3)
		ON CONFLICT (deploy_id) DO UPDATE
		SET content = EXCLUDED.content, truncated = EXCLUDED.truncated`,
		deployID, content, truncated)
	return mapErr(err)
}

// DeployLog returns stored build output. A deploy that never reached the build
// stage has no row, which is ErrNotFound rather than an empty string.
func (s *Store) DeployLog(ctx context.Context, deployID string) (string, bool, error) {
	var content string
	var truncated bool
	err := s.pool.QueryRow(ctx,
		`SELECT content, truncated FROM deploy_logs WHERE deploy_id = $1`, deployID).
		Scan(&content, &truncated)
	return content, truncated, mapErr(err)
}

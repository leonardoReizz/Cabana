package store

import (
	"context"
	"errors"

	"github.com/leonardoreis/cabana/internal/domain"
)

// BackupConfig returns the S3 destination, if one has been set. ok is false when
// no destination is configured, which the caller treats as "backups unavailable".
func (s *Store) BackupConfig(ctx context.Context) (domain.BackupConfig, bool, error) {
	var c domain.BackupConfig
	err := s.pool.QueryRow(ctx, `
		SELECT endpoint, region, bucket, access_key, secret_key, retention_count
		FROM backup_config WHERE id = true`).
		Scan(&c.Endpoint, &c.Region, &c.Bucket, &c.AccessKey, &c.SecretKey, &c.RetentionCount)
	if err != nil {
		// No destination set yet is a normal state, not an error.
		if errors.Is(mapErr(err), ErrNotFound) {
			return domain.BackupConfig{}, false, nil
		}
		return domain.BackupConfig{}, false, err
	}
	c.Configured = true
	return c, true, nil
}

func (s *Store) SaveBackupConfig(ctx context.Context, c domain.BackupConfig) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO backup_config (id, endpoint, region, bucket, access_key, secret_key, retention_count, updated_at)
		VALUES (true, $1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (id) DO UPDATE SET
			endpoint = EXCLUDED.endpoint, region = EXCLUDED.region,
			bucket = EXCLUDED.bucket, access_key = EXCLUDED.access_key,
			secret_key = EXCLUDED.secret_key, retention_count = EXCLUDED.retention_count,
			updated_at = now()`,
		c.Endpoint, c.Region, c.Bucket, c.AccessKey, c.SecretKey, c.RetentionCount)
	return err
}

func (s *Store) CreateBackup(ctx context.Context, svc domain.Service, objectKey string) (domain.Backup, error) {
	b := domain.Backup{
		ID:          domain.NewID(),
		ServiceID:   svc.ID,
		ServiceName: svc.Name,
		Template:    svc.Template,
		Status:      domain.BackupRunning,
		ObjectKey:   objectKey,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO backups (id, service_id, service_name, template, status, object_key)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`,
		b.ID, b.ServiceID, b.ServiceName, b.Template, b.Status, b.ObjectKey).Scan(&b.CreatedAt)
	return b, mapErr(err)
}

// FinishBackup records the terminal state, stamping finished_at automatically.
func (s *Store) FinishBackup(ctx context.Context, id string, status domain.BackupStatus, size int64, errMsg string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE backups SET status = $2, size_bytes = $3, error = $4, finished_at = now()
		WHERE id = $1`, id, status, size, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListBackups(ctx context.Context, serviceID string) ([]domain.Backup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, service_id, service_name, template, status, object_key,
			size_bytes, error, created_at, finished_at
		FROM backups WHERE service_id = $1 ORDER BY created_at DESC LIMIT 100`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backups := []domain.Backup{}
	for rows.Next() {
		var b domain.Backup
		if err := rows.Scan(&b.ID, &b.ServiceID, &b.ServiceName, &b.Template,
			&b.Status, &b.ObjectKey, &b.SizeBytes, &b.Error,
			&b.CreatedAt, &b.FinishedAt); err != nil {
			return nil, err
		}
		backups = append(backups, b)
	}
	return backups, rows.Err()
}

func (s *Store) BackupByID(ctx context.Context, id string) (domain.Backup, error) {
	var b domain.Backup
	err := s.pool.QueryRow(ctx, `
		SELECT id, service_id, service_name, template, status, object_key,
			size_bytes, error, created_at, finished_at
		FROM backups WHERE id = $1`, id).
		Scan(&b.ID, &b.ServiceID, &b.ServiceName, &b.Template,
			&b.Status, &b.ObjectKey, &b.SizeBytes, &b.Error,
			&b.CreatedAt, &b.FinishedAt)
	return b, mapErr(err)
}

// StaleBackups returns a service's succeeded backups beyond the newest `keep`,
// oldest first — the ones retention should prune.
func (s *Store) StaleBackups(ctx context.Context, serviceID string, keep int32) ([]domain.Backup, error) {
	if keep <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, object_key FROM backups
		WHERE service_id = $1 AND status = 'succeeded'
		ORDER BY created_at DESC OFFSET $2`, serviceID, keep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Backup
	for rows.Next() {
		var b domain.Backup
		if err := rows.Scan(&b.ID, &b.ObjectKey); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM backups WHERE id = $1`, id)
	return err
}

// ---- cabana's own state ----

func (s *Store) CreateStateBackup(ctx context.Context, objectKey string) (domain.StateBackup, error) {
	b := domain.StateBackup{ID: domain.NewID(), Status: domain.BackupRunning, ObjectKey: objectKey}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO state_backups (id, status, object_key) VALUES ($1, $2, $3)
		RETURNING created_at`, b.ID, b.Status, b.ObjectKey).Scan(&b.CreatedAt)
	return b, mapErr(err)
}

func (s *Store) FinishStateBackup(ctx context.Context, id string, status domain.BackupStatus, size int64, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE state_backups SET status = $2, size_bytes = $3, error = $4, finished_at = now()
		WHERE id = $1`, id, status, size, errMsg)
	return err
}

func (s *Store) ListStateBackups(ctx context.Context) ([]domain.StateBackup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, status, object_key, size_bytes, error, created_at, finished_at
		FROM state_backups ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backups := []domain.StateBackup{}
	for rows.Next() {
		var b domain.StateBackup
		if err := rows.Scan(&b.ID, &b.Status, &b.ObjectKey, &b.SizeBytes,
			&b.Error, &b.CreatedAt, &b.FinishedAt); err != nil {
			return nil, err
		}
		backups = append(backups, b)
	}
	return backups, rows.Err()
}

package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/leonardoreis/cabana/internal/domain"
)

func (s *Store) CreateService(ctx context.Context, svc domain.Service, secrets map[string]string) (domain.Service, error) {
	svc.ID = domain.NewID()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return svc, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
		INSERT INTO services (id, name, template, status, host, port, image, backup_schedule, backup_hour)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at, updated_at`,
		svc.ID, svc.Name, svc.Template, svc.Status, svc.Host, svc.Port, svc.Image,
		nz(svc.BackupSchedule, "off"), svc.BackupHour).
		Scan(&svc.CreatedAt, &svc.UpdatedAt)
	if err != nil {
		return svc, mapErr(err)
	}

	// Secrets are written in the same transaction as the service row: a service
	// that exists without its credentials could never be connected to.
	if len(secrets) > 0 {
		batch := &pgx.Batch{}
		for k, v := range secrets {
			batch.Queue(
				`INSERT INTO service_secrets (service_id, key, value) VALUES ($1, $2, $3)`,
				svc.ID, k, v)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return svc, err
		}
	}

	return svc, tx.Commit(ctx)
}

func (s *Store) ListServices(ctx context.Context) ([]domain.Service, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, template, status, host, port, image, backup_schedule, backup_hour, message, created_at, updated_at
		FROM services ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	services := []domain.Service{}
	for rows.Next() {
		var svc domain.Service
		if err := rows.Scan(&svc.ID, &svc.Name, &svc.Template, &svc.Status,
			&svc.Host, &svc.Port, &svc.Image, &svc.BackupSchedule, &svc.BackupHour, &svc.Message, &svc.CreatedAt, &svc.UpdatedAt); err != nil {
			return nil, err
		}
		services = append(services, svc)
	}
	return services, rows.Err()
}

func (s *Store) ServiceByID(ctx context.Context, id string) (domain.Service, error) {
	var svc domain.Service
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, template, status, host, port, image, backup_schedule, backup_hour, message, created_at, updated_at
		FROM services WHERE id = $1`, id).
		Scan(&svc.ID, &svc.Name, &svc.Template, &svc.Status,
			&svc.Host, &svc.Port, &svc.Image, &svc.BackupSchedule, &svc.BackupHour, &svc.Message, &svc.CreatedAt, &svc.UpdatedAt)
	return svc, mapErr(err)
}

func (s *Store) SetServiceStatus(ctx context.Context, id string, status domain.ServiceStatus, message string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE services SET status = $2, message = $3, updated_at = now() WHERE id = $1`,
		id, status, message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteService(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ServiceSecrets(ctx context.Context, serviceID string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, value FROM service_secrets WHERE service_id = $1`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	secrets := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		secrets[k] = v
	}
	return secrets, rows.Err()
}

// LinkService wires a service into an app. Re-linking is a no-op rather than an
// error, so the UI does not have to check first.
func (s *Store) LinkService(ctx context.Context, appID, serviceID, envVar string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_service_links (app_id, service_id, env_var)
		VALUES ($1, $2, $3)
		ON CONFLICT (app_id, service_id) DO UPDATE SET env_var = EXCLUDED.env_var`,
		appID, serviceID, envVar)
	return mapErr(err)
}

func (s *Store) UnlinkService(ctx context.Context, appID, serviceID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM app_service_links WHERE app_id = $1 AND service_id = $2`,
		appID, serviceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AppLinks(ctx context.Context, appID string) ([]domain.ServiceLink, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.service_id, s.name, s.template, l.env_var, l.created_at
		FROM app_service_links l JOIN services s ON s.id = l.service_id
		WHERE l.app_id = $1 ORDER BY s.name`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := []domain.ServiceLink{}
	for rows.Next() {
		var l domain.ServiceLink
		if err := rows.Scan(&l.ServiceID, &l.ServiceName, &l.Template,
			&l.EnvVar, &l.CreatedAt); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// LinkedServiceEnv is what gets merged into an app's environment at deploy time.
// It returns the service rows and their secrets so the caller can render each
// connection string from the template.
func (s *Store) LinkedServiceEnv(ctx context.Context, appID string) ([]LinkedService, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.name, s.template, s.host, s.port, l.env_var
		FROM app_service_links l JOIN services s ON s.id = l.service_id
		WHERE l.app_id = $1 ORDER BY s.name`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var linked []LinkedService
	for rows.Next() {
		var ls LinkedService
		if err := rows.Scan(&ls.ID, &ls.Name, &ls.Template,
			&ls.Host, &ls.Port, &ls.EnvVar); err != nil {
			return nil, err
		}
		linked = append(linked, ls)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range linked {
		secrets, err := s.ServiceSecrets(ctx, linked[i].ID)
		if err != nil {
			return nil, err
		}
		linked[i].Secrets = secrets
	}
	return linked, nil
}

type LinkedService struct {
	ID       string
	Name     string
	Template string
	Host     string
	Port     int32
	EnvVar   string
	Secrets  map[string]string
}

// ServiceLinks returns the names of apps using a service. Used to refuse a
// delete that would break a running app.
func (s *Store) ServiceLinks(ctx context.Context, serviceID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.name FROM app_service_links l
		JOIN apps a ON a.id = l.app_id
		WHERE l.service_id = $1 ORDER BY a.name`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func nz(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// SetBackupSchedule changes when a service backs itself up.
func (s *Store) SetBackupSchedule(ctx context.Context, id, schedule string, hour int32) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE services SET backup_schedule = $2, backup_hour = $3, updated_at = now()
		WHERE id = $1`, id, schedule, hour)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ScheduledService pairs a service with the time of its most recent backup, so
// the scheduler can decide whether another is due without a second query.
type ScheduledService struct {
	Service    domain.Service
	LastBackup *time.Time
}

// ScheduledServices returns every service with a schedule other than off, each
// with its last succeeded backup time.
func (s *Store) ScheduledServices(ctx context.Context) ([]ScheduledService, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.name, s.template, s.status, s.host, s.port, s.image,
			s.backup_schedule, s.backup_hour,
			(SELECT max(b.created_at) FROM backups b
			 WHERE b.service_id = s.id AND b.status = 'succeeded')
		FROM services s
		WHERE s.backup_schedule <> 'off' AND s.status = 'running'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScheduledService
	for rows.Next() {
		var ss ScheduledService
		v := &ss.Service
		if err := rows.Scan(&v.ID, &v.Name, &v.Template, &v.Status, &v.Host, &v.Port,
			&v.Image, &v.BackupSchedule, &v.BackupHour, &ss.LastBackup); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

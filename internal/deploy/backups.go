package deploy

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/leonardoreis/cabana/internal/catalog"
	"github.com/leonardoreis/cabana/internal/domain"
)

// S3Config is the backup destination. S3-compatible so R2, B2, MinIO and AWS
// all work with the same fields.
type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// awsImage carries both the aws CLI and coreutils, which is all the S3 side of
// a backup needs. Pinned so a backup that worked keeps working.
const awsImage = "amazon/aws-cli:2.17.61"

const backupSecretName = "cabana-backup-s3"

// backupMount is the shared scratch volume the dump and upload steps hand a file
// across. emptyDir, not a PVC: it lives only for the length of the Job.
const backupPath = "/backup/dump"

var sizeMarker = regexp.MustCompile(`CABANA_BACKUP_SIZE=(\d+)`)

// BackupService dumps a service and ships the dump to S3, returning the stored
// size. It blocks until the Job finishes.
//
// Two steps in one pod: an init container runs the template's dump command in
// the service's own image (which has the client tools) writing to a shared
// volume, then the main container uploads that file. Splitting them keeps
// cabana out of the data path — it never holds the dump, only orchestrates.
func (d *K3s) BackupService(
	ctx context.Context, svc domain.Service, tmpl catalog.Template, s3 S3Config, objectKey string,
) (int64, error) {
	if !tmpl.Backupable() {
		return 0, fmt.Errorf("%s does not support backup", tmpl.Name)
	}
	ns := serviceNamespace(svc)
	if err := d.ensureBackupSecret(ctx, ns, s3); err != nil {
		return 0, fmt.Errorf("s3 credentials: %w", err)
	}

	name := "backup-" + backupSuffix(objectKey)
	dump := d.dumpContainer(svc, tmpl)
	upload := d.s3Container("upload", s3, []string{"sh", "-c",
		// addressing_style path is what makes MinIO and B2 work; virtual-host
		// style assumes a bucket subdomain they do not serve.
		`aws configure set default.s3.addressing_style path && ` +
			`aws --endpoint-url "$S3_ENDPOINT" s3 cp ` + backupPath + ` "s3://$S3_BUCKET/$S3_KEY" && ` +
			`echo "CABANA_BACKUP_SIZE=$(stat -c%s ` + backupPath + `)"`}, objectKey)

	job := backupJob(name, svc, []corev1.Container{dump}, upload)
	if err := d.runBackupJob(ctx, ns, job); err != nil {
		return 0, err
	}
	return d.readBackupSize(ctx, ns, name), nil
}

// RestoreService pulls a dump from S3 and replays it into the running service.
// Destructive by design — the restore command drops and recreates.
func (d *K3s) RestoreService(
	ctx context.Context, svc domain.Service, tmpl catalog.Template, s3 S3Config, objectKey string,
) error {
	if !tmpl.Backupable() {
		return fmt.Errorf("%s does not support restore", tmpl.Name)
	}
	ns := serviceNamespace(svc)
	if err := d.ensureBackupSecret(ctx, ns, s3); err != nil {
		return fmt.Errorf("s3 credentials: %w", err)
	}

	name := "restore-" + backupSuffix(objectKey)
	download := d.s3Container("download", s3, []string{"sh", "-c",
		`aws configure set default.s3.addressing_style path && ` +
			`aws --endpoint-url "$S3_ENDPOINT" s3 cp "s3://$S3_BUCKET/$S3_KEY" ` + backupPath}, objectKey)
	restore := d.restoreContainer(svc, tmpl)

	// Download is the init step, restore the main one — the file must exist
	// before the restore command reads it.
	job := backupJob(name, svc, []corev1.Container{download}, restore)
	return d.runBackupJob(ctx, ns, job)
}

func (d *K3s) ensureBackupSecret(ctx context.Context, ns string, s3 S3Config) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: backupSecretName},
		StringData: map[string]string{
			"AWS_ACCESS_KEY_ID":     s3.AccessKey,
			"AWS_SECRET_ACCESS_KEY": s3.SecretKey,
		},
	}
	secrets := d.client.CoreV1().Secrets(ns)
	_, err := secrets.Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// Credentials can change (a rotated key), so unlike a service's own
		// secret this one is updated.
		_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	}
	return err
}

// dumpContainer runs the template's dump command in the service image, with the
// service password and address in the environment the command expects.
func (d *K3s) dumpContainer(svc domain.Service, tmpl catalog.Template) corev1.Container {
	return corev1.Container{
		Name:         "dump",
		Image:        serviceImage(svc, tmpl),
		Command:      tmpl.Backup.Dump,
		Env:          backupEnv(svc),
		VolumeMounts: []corev1.VolumeMount{{Name: "work", MountPath: "/backup"}},
	}
}

func (d *K3s) restoreContainer(svc domain.Service, tmpl catalog.Template) corev1.Container {
	return corev1.Container{
		Name:         "restore",
		Image:        serviceImage(svc, tmpl),
		Command:      tmpl.Backup.Restore,
		Env:          backupEnv(svc),
		VolumeMounts: []corev1.VolumeMount{{Name: "work", MountPath: "/backup"}},
	}
}

func (d *K3s) s3Container(name string, s3 S3Config, command []string, objectKey string) corev1.Container {
	return corev1.Container{
		Name:    name,
		Image:   awsImage,
		Command: command,
		Env: []corev1.EnvVar{
			secretEnv("AWS_ACCESS_KEY_ID", backupSecretName, "AWS_ACCESS_KEY_ID"),
			secretEnv("AWS_SECRET_ACCESS_KEY", backupSecretName, "AWS_SECRET_ACCESS_KEY"),
			{Name: "AWS_DEFAULT_REGION", Value: s3.Region},
			// aws-cli otherwise probes the EC2 metadata endpoint on every call,
			// adding a slow timeout on a non-EC2 host.
			{Name: "AWS_EC2_METADATA_DISABLED", Value: "true"},
			{Name: "S3_ENDPOINT", Value: s3.Endpoint},
			{Name: "S3_BUCKET", Value: s3.Bucket},
			{Name: "S3_KEY", Value: objectKey},
		},
		VolumeMounts: []corev1.VolumeMount{{Name: "work", MountPath: "/backup"}},
	}
}

// backupEnv gives a dump/restore command the two things every one of them needs:
// the service address and its password. The password rides from the service's
// own Secret rather than being written into the Job spec.
func backupEnv(svc domain.Service) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "BACKUP_HOST", Value: svc.Host},
		{Name: "BACKUP_FILE", Value: backupPath},
		secretEnv("PASSWORD", serviceSecretName, "password"),
	}
}

func backupJob(name string, svc domain.Service, initContainers []corev1.Container, main corev1.Container) *batchv1.Job {
	backoff := int32(0) // A failed backup is reported, never silently retried.
	ttl := int32(3600)
	labels := map[string]string{
		managedByLabel:          "cabana",
		componentLabel:          "backup",
		"cabana.dev/service-id": svc.ID,
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:  corev1.RestartPolicyNever,
					InitContainers: initContainers,
					Containers:     []corev1.Container{main},
					Volumes: []corev1.Volume{{
						Name:         "work",
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
					}},
				},
			},
		},
	}
}

func (d *K3s) runBackupJob(ctx context.Context, ns string, job *batchv1.Job) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	if _, err := d.client.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	if err := d.waitForJob(ctx, ns, job.Name); err != nil {
		// The pod logs carry the actual command failure; the Job condition is
		// only "failed". Surface the tail so a bad password or unreachable
		// bucket is legible.
		if tail := d.backupFailureDetail(ctx, ns, job.Name); tail != "" {
			return fmt.Errorf("%w: %s", err, tail)
		}
		return err
	}
	return nil
}

// readBackupSize parses the size the upload step printed. Best-effort: a missing
// size does not fail an otherwise good backup, it just records zero.
func (d *K3s) readBackupSize(ctx context.Context, ns, jobName string) int64 {
	logs := d.jobContainerLogs(ctx, ns, jobName, "upload")
	if m := sizeMarker.FindStringSubmatch(logs); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func (d *K3s) backupFailureDetail(ctx context.Context, ns, jobName string) string {
	// Whichever container failed, its last lines explain why.
	for _, c := range []string{"dump", "restore", "upload", "download"} {
		if logs := lastLines(d.jobContainerLogs(ctx, ns, jobName, c), 3); logs != "" {
			return logs
		}
	}
	return ""
}

func (d *K3s) jobContainerLogs(ctx context.Context, ns, jobName, container string) string {
	pods, err := d.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	stream, err := d.client.CoreV1().Pods(ns).
		GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{Container: container}).Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()

	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil || len(out) > 1<<16 {
			break
		}
	}
	return string(out)
}

func serviceImage(svc domain.Service, tmpl catalog.Template) string {
	if svc.Image != "" {
		return svc.Image
	}
	return tmpl.Image
}

// backupSuffix derives a Job name fragment from the object key. The key ends in
// "<service>/<id>.dump"; the id is unique and DNS-safe, which the key's slashes
// and dots are not.
func backupSuffix(objectKey string) string {
	base := objectKey
	if i := lastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if i := lastIndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	return base
}

func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func lastLines(s string, n int) string {
	sc := bufio.NewScanner(strings.NewReader(s))
	var lines []string
	for sc.Scan() {
		if t := sc.Text(); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "; "
		}
		out += l
	}
	return out
}

// stateNamespace is where cabana's own supporting workloads live, and where the
// state-backup Job runs so it can reach the cabana database over cluster DNS.
const stateNamespace = "cabana-system"

// PruneObjects deletes backup objects from S3 that retention has aged out. Runs
// as a short aws-cli Job in the service's namespace, where the S3 secret already
// exists from the backup that triggered this.
func (d *K3s) PruneObjects(ctx context.Context, svc domain.Service, s3 S3Config, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	ns := serviceNamespace(svc)
	if err := d.ensureBackupSecret(ctx, ns, s3); err != nil {
		return err
	}

	name := "prune-" + domain.NewID()[:12]
	remover := d.s3Container("prune", s3, []string{"sh", "-c",
		`aws configure set default.s3.addressing_style path; ` +
			`for k in $S3_KEYS; do aws --endpoint-url "$S3_ENDPOINT" s3 rm "s3://$S3_BUCKET/$k"; done`}, "")
	remover.Env = append(remover.Env, corev1.EnvVar{Name: "S3_KEYS", Value: strings.Join(keys, " ")})
	remover.VolumeMounts = nil

	job := pruneJob(name, svc, remover)
	if _, err := d.client.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create prune job: %w", err)
	}
	pruneCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return d.waitForJob(pruneCtx, ns, name)
}

func pruneJob(name string, svc domain.Service, main corev1.Container) *batchv1.Job {
	backoff := int32(0)
	ttl := int32(600)
	labels := map[string]string{
		managedByLabel:          "cabana",
		componentLabel:          "backup",
		"cabana.dev/service-id": svc.ID,
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{main},
				},
			},
		},
	}
}

// StateDB is the connection to cabana's own database, as reachable from inside
// the cluster — which differs from cabana's own DSN, since cabana talks to it
// over a host-local NodePort a pod cannot use.
type StateDB struct {
	Host, Port, User, Password, Name, Image string
}

// BackupState dumps cabana's own database to S3. This is the disaster-recovery
// copy: restored onto a fresh install, it brings the whole platform back.
func (d *K3s) BackupState(ctx context.Context, db StateDB, s3 S3Config, objectKey string) (int64, error) {
	if err := d.ensureBackupSecret(ctx, stateNamespace, s3); err != nil {
		return 0, fmt.Errorf("s3 credentials: %w", err)
	}
	if err := d.ensureStateDBSecret(ctx, db); err != nil {
		return 0, fmt.Errorf("db credentials: %w", err)
	}

	name := "state-backup-" + backupSuffix(objectKey)
	dump := corev1.Container{
		Name:  "dump",
		Image: db.Image,
		Command: []string{"sh", "-c",
			`pg_dump -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -Fc -f ` + backupPath},
		Env: []corev1.EnvVar{
			{Name: "DB_HOST", Value: db.Host}, {Name: "DB_PORT", Value: db.Port},
			{Name: "DB_USER", Value: db.User}, {Name: "DB_NAME", Value: db.Name},
			secretEnv("PGPASSWORD", stateDBSecretName, "password"),
		},
		VolumeMounts: []corev1.VolumeMount{{Name: "work", MountPath: "/backup"}},
	}
	upload := d.s3Container("upload", s3, []string{"sh", "-c",
		`aws configure set default.s3.addressing_style path && ` +
			`aws --endpoint-url "$S3_ENDPOINT" s3 cp ` + backupPath + ` "s3://$S3_BUCKET/$S3_KEY" && ` +
			`echo "CABANA_BACKUP_SIZE=$(stat -c%s ` + backupPath + `)"`}, objectKey)

	job := backupJob(name, domain.Service{ID: "cabana-state"}, []corev1.Container{dump}, upload)
	job.Namespace = stateNamespace
	if err := d.runStateJob(ctx, job); err != nil {
		return 0, err
	}
	return d.readBackupSize(ctx, stateNamespace, name), nil
}

const stateDBSecretName = "cabana-state-db"

func (d *K3s) ensureStateDBSecret(ctx context.Context, db StateDB) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: stateDBSecretName},
		StringData: map[string]string{"password": db.Password},
	}
	secrets := d.client.CoreV1().Secrets(stateNamespace)
	_, err := secrets.Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	}
	return err
}

func (d *K3s) runStateJob(ctx context.Context, job *batchv1.Job) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if _, err := d.client.BatchV1().Jobs(stateNamespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	if err := d.waitForJob(ctx, stateNamespace, job.Name); err != nil {
		if tail := d.backupFailureDetail(ctx, stateNamespace, job.Name); tail != "" {
			return fmt.Errorf("%w: %s", err, tail)
		}
		return err
	}
	return nil
}

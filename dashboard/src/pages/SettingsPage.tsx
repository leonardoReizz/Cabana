import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { ApiError, api, type BackupConfig } from '../api/client'
import { backupConfigSchema, type BackupConfigInput } from '../schemas'
import { Button } from '../ui/Button'
import { Field } from '../ui/Field'
import { StateBackupSection } from './StateBackupSection'
import styles from './SettingsPage.module.css'

/**
 * Install-wide settings. Today that is the backup destination; it lives on its
 * own page rather than per-service because one S3 bucket serves the whole
 * install.
 */
export function SettingsPage() {
  const [config, setConfig] = useState<BackupConfig | null>(null)
  const [saved, setSaved] = useState(false)
  const [formError, setFormError] = useState('')

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<BackupConfigInput>({
    resolver: zodResolver(backupConfigSchema),
    defaultValues: {
      endpoint: '',
      region: 'auto',
      bucket: '',
      access_key: '',
      secret_key: '',
      retention_count: 0,
    },
  })

  useEffect(() => {
    api
      .getBackupConfig()
      .then((cfg) => {
        setConfig(cfg)
        // The secret is never returned, so its field stays empty and a save
        // that leaves it empty keeps the stored one.
        reset({
          endpoint: cfg.endpoint,
          region: cfg.region || 'auto',
          bucket: cfg.bucket,
          access_key: cfg.access_key,
          secret_key: '',
          retention_count: cfg.retention_count,
        })
      })
      .catch(() => setFormError('Could not load settings.'))
  }, [reset])

  async function onSubmit(values: BackupConfigInput) {
    setFormError('')
    try {
      const cfg = await api.saveBackupConfig(values)
      setConfig(cfg)
      setSaved(true)
    } catch (err) {
      setFormError(
        err instanceof ApiError ? err.message : 'Could not save settings.',
      )
    }
  }

  return (
    <div className={styles.page}>
      <div className={styles.head}>
        <h1 className={styles.title}>Settings</h1>
        <p className={styles.subtitle}>Configuration for the whole install.</p>
      </div>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Backup destination</h2>
        <p className={styles.sectionLede}>
          An S3-compatible bucket — Cloudflare R2, Backblaze B2, MinIO or AWS.
          Once set, any database service can be backed up from its page.
        </p>

        <form className={styles.form} onSubmit={handleSubmit(onSubmit)} noValidate>
          {formError && (
            <div className={styles.error} role="alert">
              {formError}
            </div>
          )}

          <Field
            label="Endpoint"
            placeholder="https://s3.example.com"
            hint="The S3 API URL. Include http:// or https://."
            mono
            error={errors.endpoint?.message}
            {...register('endpoint', { onChange: () => setSaved(false) })}
          />

          <div className={styles.grid2}>
            <Field
              label="Bucket"
              placeholder="cabana-backups"
              mono
              error={errors.bucket?.message}
              {...register('bucket', { onChange: () => setSaved(false) })}
            />
            <Field
              label="Region"
              placeholder="auto"
              hint="R2 uses auto; AWS uses e.g. us-east-1."
              mono
              error={errors.region?.message}
              {...register('region', { onChange: () => setSaved(false) })}
            />
          </div>

          <div className={styles.grid2}>
            <Field
              label="Access key"
              mono
              autoComplete="off"
              error={errors.access_key?.message}
              {...register('access_key', { onChange: () => setSaved(false) })}
            />
            <Field
              label="Secret key"
              type="password"
              autoComplete="off"
              hint={
                config?.configured
                  ? 'Leave blank to keep the stored one.'
                  : undefined
              }
              error={errors.secret_key?.message}
              {...register('secret_key', { onChange: () => setSaved(false) })}
            />
          </div>

          <Field
            label="Keep per service"
            type="number"
            min={0}
            max={1000}
            hint="How many backups to keep for each service. 0 keeps them all; older ones are deleted from the bucket."
            error={errors.retention_count?.message}
            {...register('retention_count', {
              valueAsNumber: true,
              onChange: () => setSaved(false),
            })}
          />

          <div className={styles.footer}>
            <span
              className={`${styles.status} ${
                config?.configured ? styles.configured : ''
              }`}
            >
              {saved
                ? 'Saved.'
                : config?.configured
                  ? '● A destination is configured.'
                  : 'No destination set yet.'}
            </span>
            <Button type="submit" variant="primary" loading={isSubmitting}>
              Save
            </Button>
          </div>
        </form>
      </section>

      <StateBackupSection />
    </div>
  )
}

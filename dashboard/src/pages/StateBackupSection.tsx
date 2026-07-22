import { useCallback, useEffect, useState } from 'react'
import { ApiError, api, type StateBackup } from '../api/client'
import { Button } from '../ui/Button'
import { StatusBadge } from '../ui/StatusBadge'
import styles from './SettingsPage.module.css'

/**
 * Backup of cabana's own state — the apps, services, links and secrets that
 * define this install. It is the disaster-recovery copy: restored onto a fresh
 * machine, the platform comes back.
 */
export function StateBackupSection() {
  const [backups, setBackups] = useState<StateBackup[]>([])
  const [available, setAvailable] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const res = await api.listStateBackups()
      setBackups(res.backups)
      setAvailable(res.available)
    } catch {
      setError('Could not load state backups.')
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const running = backups.some((b) => b.status === 'running')
  useEffect(() => {
    if (!running) return
    const timer = setInterval(() => void refresh(), 3000)
    return () => clearInterval(timer)
  }, [running, refresh])

  async function backupNow() {
    setBusy(true)
    setError('')
    try {
      await api.createStateBackup()
      await refresh()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not start backup.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className={styles.section} style={{ marginTop: 'var(--space-5)' }}>
      <h2 className={styles.sectionTitle}>cabana&rsquo;s own state</h2>
      <p className={styles.sectionLede}>
        A dump of cabana&rsquo;s database — every app, service, link and stored
        credential. Keep it to recover the whole platform if the server is lost.
      </p>

      {!available ? (
        <p className={styles.status}>
          State backup is not enabled on this install. It is turned on by the
          installer for a production deployment.
        </p>
      ) : (
        <>
          <div className={styles.footer} style={{ marginBottom: 'var(--space-4)' }}>
            <span className={styles.status}>
              {backups.length === 0
                ? 'No state backups yet.'
                : `${backups.length} state backup${backups.length === 1 ? '' : 's'}`}
            </span>
            <Button
              variant="primary"
              onClick={() => void backupNow()}
              loading={busy}
            >
              Back up state now
            </Button>
          </div>

          {error && <p className={styles.error}>{error}</p>}

          {backups.map((b) => (
            <div key={b.id} className={styles.stateRow}>
              <StatusBadge status={b.status} />
              <span className={styles.stateId}>{b.id.slice(0, 8)}</span>
              <span className={styles.stateTime}>
                {new Date(b.created_at).toLocaleString()}
              </span>
              <span className={styles.stateTime}>
                {b.status === 'succeeded' ? formatBytes(b.size_bytes) : ''}
                {b.status === 'failed' && b.error ? (
                  <span title={b.error}>failed</span>
                ) : null}
              </span>
            </div>
          ))}
        </>
      )}
    </section>
  )
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

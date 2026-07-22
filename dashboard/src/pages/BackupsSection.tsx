import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  ApiError,
  api,
  type Backup,
  type ServiceStatus,
} from '../api/client'
import { Button } from '../ui/Button'
import { Select } from '../ui/Select'
import { StatusBadge } from '../ui/StatusBadge'
import { useConfirm } from '../ui/AlertDialog'
import styles from './ServiceDetailPage.module.css'

/**
 * Backup and restore for one service.
 *
 * Restore is guarded hard: it overwrites live data, and the aggressive
 * confirmation spells that out rather than treating it like an ordinary action.
 */
export function BackupsSection({
  serviceId,
  status,
  schedule: initialSchedule,
  hour: initialHour,
}: {
  serviceId: string
  status: ServiceStatus
  schedule: 'off' | 'daily' | 'weekly'
  hour: number
}) {
  const confirm = useConfirm()
  const [schedule, setSchedule] = useState<string>(initialSchedule)
  const [hour, setHour] = useState(initialHour)
  const [backups, setBackups] = useState<Backup[]>([])
  const [backupable, setBackupable] = useState(false)
  const [busy, setBusy] = useState(false)
  const [restoring, setRestoring] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const refresh = useCallback(async () => {
    try {
      const res = await api.listBackups(serviceId)
      setBackups(res.backups)
      setBackupable(res.backupable)
    } catch {
      setError('Could not load backups.')
    }
  }, [serviceId])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // Poll while a backup is still running.
  const running = backups.some((b) => b.status === 'running')
  useEffect(() => {
    if (!running) return
    const timer = setInterval(() => void refresh(), 3000)
    return () => clearInterval(timer)
  }, [running, refresh])

  async function changeSchedule(nextSchedule: string, nextHour: number) {
    setSchedule(nextSchedule)
    setHour(nextHour)
    setError('')
    try {
      await api.setBackupSchedule(serviceId, nextSchedule, nextHour)
      setNotice(
        nextSchedule === 'off'
          ? 'Automatic backups off.'
          : `Backing up ${nextSchedule} around ${String(nextHour).padStart(2, '0')}:00 UTC.`,
      )
    } catch (err) {
      // Roll the control back to what the server still has.
      setSchedule(initialSchedule)
      setError(err instanceof ApiError ? err.message : 'Could not set schedule.')
    }
  }

  // A service that cannot be backed up (a cache, a UI) simply hides the panel.
  if (!backupable) return null

  async function backupNow() {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      await api.createBackup(serviceId)
      setNotice('Backup started.')
      await refresh()
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.code === 'no_destination'
            ? 'No backup destination is set.'
            : err.message
          : 'Could not start backup.',
      )
    } finally {
      setBusy(false)
    }
  }

  async function restore(backup: Backup) {
    const confirmed = await confirm({
      title: `Restore ${backup.id.slice(0, 8)}?`,
      description: `This replays the backup from ${new Date(
        backup.created_at,
      ).toLocaleString()} into the running service.`,
      consequence:
        'Everything currently in this service is dropped and replaced by the backup. Any data written since then is lost, and this cannot be undone.',
      confirmLabel: 'Overwrite and restore',
    })
    if (!confirmed) return

    setRestoring(backup.id)
    setError('')
    setNotice('')
    try {
      await api.restoreBackup(serviceId, backup.id)
      setNotice('Restore complete.')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Restore failed.')
    } finally {
      setRestoring('')
    }
  }

  return (
    <section className={styles.section}>
      <div className={styles.backupHead}>
        <h2 className={styles.sectionTitle}>Backups</h2>
        <div className={styles.backupControls}>
          <Select
            value={schedule}
            onChange={(v) => void changeSchedule(v, hour)}
            ariaLabel="Backup schedule"
            options={[
              { value: 'off', label: 'Manual only' },
              { value: 'daily', label: 'Daily' },
              { value: 'weekly', label: 'Weekly' },
            ]}
          />
          {schedule !== 'off' && (
            <Select
              value={String(hour)}
              onChange={(v) => void changeSchedule(schedule, Number(v))}
              ariaLabel="Backup hour (UTC)"
              options={Array.from({ length: 24 }, (_, h) => ({
                value: String(h),
                label: `${String(h).padStart(2, '0')}:00 UTC`,
              }))}
            />
          )}
          <Button
            size="sm"
            variant="primary"
            onClick={() => void backupNow()}
            loading={busy}
            disabled={status !== 'running'}
          >
            Back up now
          </Button>
        </div>
      </div>

      {(error || notice) && (
        <p className={error ? styles.error : styles.note}>{error || notice}</p>
      )}

      <div className={styles.panel}>
        {backups.length === 0 ? (
          <p className={styles.logEmpty} style={{ padding: 'var(--space-4)' }}>
            No backups yet.{' '}
            <Link to="/settings">Set a destination</Link> if you have not, then
            press Back up now.
          </p>
        ) : (
          backups.map((b) => (
            <div key={b.id} className={styles.row}>
              <StatusBadge status={b.status} />
              <span className={styles.value}>{b.id.slice(0, 8)}</span>
              <span className={styles.rowTime}>
                {new Date(b.created_at).toLocaleString()}
              </span>
              <span className={styles.rowTime}>
                {b.status === 'succeeded' ? formatBytes(b.size_bytes) : ''}
              </span>
              <span style={{ flex: 1 }} />
              {b.status === 'succeeded' && (
                <Button
                  size="sm"
                  variant="danger"
                  onClick={() => void restore(b)}
                  disabled={restoring !== '' || status !== 'running'}
                  loading={restoring === b.id}
                >
                  Restore
                </Button>
              )}
              {b.status === 'failed' && b.error && (
                <span className={styles.rowTime} title={b.error}>
                  failed
                </span>
              )}
            </div>
          ))
        )}
      </div>
    </section>
  )
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

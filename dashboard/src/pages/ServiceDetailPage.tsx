import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  ApiError,
  api,
  streamServiceLogs,
  type LogLine,
  type Service,
  type ServiceCredentials,
} from '../api/client'
import { Button } from '../ui/Button'
import { useConfirm } from '../ui/AlertDialog'
import { BackupsSection } from './BackupsSection'
import { CopyButton } from '../ui/CopyButton'
import { StatusBadge } from '../ui/StatusBadge'
import styles from './ServiceDetailPage.module.css'

export function ServiceDetailPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const confirm = useConfirm()

  const [service, setService] = useState<Service | null>(null)
  const [creds, setCreds] = useState<ServiceCredentials | null>(null)
  const [error, setError] = useState('')
  const [revealed, setRevealed] = useState<Record<string, boolean>>({})

  const refresh = useCallback(async () => {
    try {
      const svc = await api.getService(id)
      setService(svc)
      // Credentials only exist once the service has been provisioned.
      if (svc.status !== 'provisioning') {
        setCreds(await api.serviceCredentials(id))
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not load service.')
    }
  }, [id])

  useEffect(() => {
    void refresh()
  }, [refresh])

  useEffect(() => {
    if (service?.status !== 'provisioning') return
    const timer = setInterval(() => void refresh(), 2000)
    return () => clearInterval(timer)
  }, [service, refresh])

  async function handleDelete() {
    if (!service) return
    const confirmed = await confirm({
      title: `Delete ${service.name}?`,
      description: `This removes the ${service.template} container and its storage volume.`,
      consequence:
        'Every database, key and file it holds is destroyed. There is no backup, and this cannot be undone.',
      confirmLabel: 'Delete service',
    })
    if (!confirmed) return
    try {
      await api.deleteService(id)
      void navigate('/services')
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : 'Could not delete the service.',
      )
    }
  }

  if (!service) return error ? <p role="alert">{error}</p> : null

  return (
    <>
      <Link to="/services" className={styles.back}>
        ← Services
      </Link>

      <div className={styles.head}>
        <div>
          <div className={styles.titleRow}>
            <h1 className={styles.title}>{service.name}</h1>
            <StatusBadge status={service.status} />
          </div>
          <p className={styles.template}>{service.image || service.template}</p>
        </div>
        <Button variant="danger" onClick={() => void handleDelete()}>
          Delete
        </Button>
      </div>

      {error && <p className={styles.error}>{error}</p>}
      {service.status === 'failed' && service.message && (
        <p className={styles.error}>{service.message}</p>
      )}

      {creds && (
        <>
          <section className={styles.section}>
            <h2 className={styles.sectionTitle}>Connection</h2>
            <p className={styles.note}>
              Link this service to an app from the app's page and{' '}
              <code>{creds.env_var}</code> is injected automatically — you should
              not need to paste anything below.
            </p>
            <div className={styles.panel}>
              <div className={styles.row}>
                <span className={styles.label}>{creds.env_var}</span>
                <span className={styles.value}>{creds.connection_string}</span>
                <CopyButton value={creds.connection_string} />
              </div>
              <div className={styles.row}>
                <span className={styles.label}>Host</span>
                <span className={styles.value}>{creds.host}</span>
                <CopyButton value={creds.host} />
              </div>
              <div className={styles.row}>
                <span className={styles.label}>Port</span>
                <span className={styles.value}>{creds.port}</span>
                <CopyButton value={String(creds.port)} />
              </div>
            </div>
          </section>

          <section className={styles.section}>
            <h2 className={styles.sectionTitle}>Credentials</h2>
            <div className={styles.panel}>
              {Object.entries(creds.secrets).map(([key, value]) => (
                <div key={key} className={styles.row}>
                  <span className={styles.label}>{key}</span>
                  {revealed[key] ? (
                    <span className={styles.value}>{value}</span>
                  ) : (
                    <span className={styles.masked}>{'•'.repeat(24)}</span>
                  )}
                  <span className={styles.secretControls}>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() =>
                        setRevealed((prev) => ({ ...prev, [key]: !prev[key] }))
                      }
                    >
                      {revealed[key] ? 'Hide' : 'Reveal'}
                    </Button>
                    <CopyButton value={value} />
                  </span>
                </div>
              ))}
            </div>
          </section>
        </>
      )}

      <BackupsSection
        serviceId={id}
        status={service.status}
        schedule={service.backup_schedule}
        hour={service.backup_hour}
      />

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Logs</h2>
        {service.status === 'provisioning' && (
          <p className={styles.note}>
            Installing. Credentials appear once it is running.
          </p>
        )}
        <LogTerminal serviceId={id} />
      </section>
    </>
  )
}

/**
 * Provisioning narration and container output, in one stream.
 *
 * Recent history is replayed on connect, so opening this page after an install
 * still shows how it went — including a failure that happened while nobody was
 * watching.
 */
function LogTerminal({ serviceId }: { serviceId: string }) {
  const [lines, setLines] = useState<LogLine[]>([])
  const boxRef = useRef<HTMLDivElement>(null)
  const pinnedRef = useRef(true)

  useEffect(() => {
    setLines([])
    return streamServiceLogs(serviceId, (line) =>
      setLines((prev) => [...prev, line].slice(-1000)),
    )
  }, [serviceId])

  // Follow the tail only while the user is already at the bottom, so scrolling
  // back to read something does not yank them forward.
  useEffect(() => {
    const box = boxRef.current
    if (box && pinnedRef.current) box.scrollTop = box.scrollHeight
  }, [lines])

  function handleScroll() {
    const box = boxRef.current
    if (!box) return
    pinnedRef.current = box.scrollHeight - box.scrollTop - box.clientHeight < 40
  }

  return (
    <div ref={boxRef} className={styles.terminal} onScroll={handleScroll}>
      {lines.length === 0 && (
        <p className={styles.logEmpty}>Waiting for output…</p>
      )}
      {lines.map((line, i) => (
        <div key={i} className={styles.logLine}>
          <span className={styles.logTime}>
            {new Date(line.ts).toLocaleTimeString()}
          </span>
          <span
            className={
              line.source === 'app'
                ? `${styles.logSource} ${styles.logSourceApp}`
                : styles.logSource
            }
          >
            {line.source === 'app' ? 'service' : line.source}
          </span>
          <span className={styles.logText}>{line.text}</span>
        </div>
      ))}
    </div>
  )
}

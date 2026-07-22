import { useEffect, useRef, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import {
  ApiError,
  api,
  streamLogs,
  type AppStatus,
  type Deploy,
  type DeployStatus,
  type LogLine,
} from '../../api/client'
import { Button } from '../../ui/Button'
import { Select } from '../../ui/Select'
import { StatusBadge } from '../../ui/StatusBadge'
import { useConfirm } from '../../ui/AlertDialog'
import type { AppTabContext } from './AppDetailPage'
import styles from './app.module.css'

type LogTab = 'app' | 'build'

/** Everything about what ran and when: live output, stored builds, history. */
export function LogsTab() {
  const { app, deploys, refresh } = useOutletContext<AppTabContext>()
  return (
    <>
      <LogSection appId={app.id} deploys={deploys} appStatus={app.status} />
      <DeploysSection appId={app.id} deploys={deploys} refresh={refresh} />
    </>
  )
}

/**
 * Log panel with two views over one stream.
 *
 * A single EventSource feeds both tabs — switching tabs must not reconnect,
 * because a reconnect replays the buffer and looks like duplicated output.
 *
 * The build tab prefers the *stored* log for any finished deploy, so a build
 * from yesterday reads exactly the same as one from a minute ago. It only falls
 * back to the live stream while a deploy is actually in flight.
 */
function LogSection({
  appId,
  deploys,
  appStatus,
}: {
  appId: string
  deploys: Deploy[]
  appStatus: AppStatus
}) {
  const [tab, setTab] = useState<LogTab>('app')
  const [lines, setLines] = useState<LogLine[]>([])

  useEffect(() => {
    setLines([])
    // Cap the buffer: an app left open overnight should not grow the tab's
    // memory without bound.
    return streamLogs(appId, (line) =>
      setLines((prev) => [...prev, line].slice(-1000)),
    )
  }, [appId])

  // cabana's own notices belong with the app log — they are the narration of
  // what happened to it.
  const appLines = lines.filter((l) => l.source !== 'build')

  return (
    <section className={styles.section}>
      <div className={styles.sectionHead}>
        <div className={styles.tabs}>
          <button
            type="button"
            className={`${styles.tab} ${tab === 'app' ? styles.tabActive : ''}`}
            onClick={() => setTab('app')}
            aria-pressed={tab === 'app'}
          >
            Logs
          </button>
          <button
            type="button"
            className={`${styles.tab} ${tab === 'build' ? styles.tabActive : ''}`}
            onClick={() => setTab('build')}
            aria-pressed={tab === 'build'}
          >
            Build
          </button>
        </div>
      </div>

      {tab === 'app' ? (
        <LogTerminal lines={appLines} empty="Waiting for output…" />
      ) : (
        <BuildLog
          appId={appId}
          deploys={deploys}
          liveLines={lines.filter((l) => l.source === 'build')}
          appStatus={appStatus}
        />
      )}
    </section>
  )
}

const IN_FLIGHT_DEPLOY = new Set<DeployStatus>([
  'queued',
  'building',
  'deploying',
])

function BuildLog({
  appId,
  deploys,
  liveLines,
  appStatus,
}: {
  appId: string
  deploys: Deploy[]
  liveLines: LogLine[]
  appStatus: AppStatus
}) {
  const latest = deploys[0]
  const [selectedId, setSelectedId] = useState<string>('')
  const [stored, setStored] = useState<{
    log: string
    truncated: boolean
  } | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  // Default to the newest deploy, and follow it as new ones arrive rather than
  // stranding the user on an old one.
  const deployId = selectedId || latest?.id || ''
  const selected = deploys.find((d) => d.id === deployId)
  const live = selected ? IN_FLIGHT_DEPLOY.has(selected.status) : false

  useEffect(() => {
    if (!deployId || live) {
      setStored(null)
      return
    }
    let cancelled = false
    setLoading(true)
    setError('')
    api
      .deployLog(appId, deployId)
      .then((res) => {
        if (!cancelled) setStored({ log: res.log, truncated: res.truncated })
      })
      .catch(() => {
        if (!cancelled) setError('Could not load this build log.')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
    // appStatus is a dependency so the stored log is re-fetched once a deploy
    // that was in flight finishes and its log lands in the database.
  }, [appId, deployId, live, appStatus])

  if (!latest) {
    return (
      <div className={styles.terminal}>
        <p className={styles.logEmpty}>No deploys yet.</p>
      </div>
    )
  }

  const storedLines: LogLine[] = stored
    ? stored.log
        .split('\n')
        .filter((text) => text.length > 0)
        .map((text) => ({ ts: '', source: 'build' as const, text }))
    : []

  return (
    <>
      <div className={styles.sectionHead}>
        <span className={styles.tabMeta}>
          <span>Deploy</span>
          <Select
            value={deployId}
            onChange={setSelectedId}
            ariaLabel="Deploy to show the build log for"
            options={deploys.map((d, i) => ({
              value: d.id,
              label: `${d.id.slice(0, 8)} · ${d.status}${i === 0 ? ' · latest' : ''}`,
            }))}
          />
          {live && <span>streaming…</span>}
        </span>
      </div>

      {stored?.truncated && (
        <p className={styles.truncated}>
          This build produced more output than cabana stores. Showing the end of
          it, which is where a failure is reported.
        </p>
      )}

      <LogTerminal
        lines={live ? liveLines : storedLines}
        showTime={live}
        empty={
          error
            ? error
            : loading
              ? 'Loading…'
              : live
                ? 'Waiting for the build to start…'
                : 'No build output was stored for this deploy.'
        }
      />
    </>
  )
}

function LogTerminal({
  lines,
  empty,
  showTime = true,
}: {
  lines: LogLine[]
  empty: string
  showTime?: boolean
}) {
  const boxRef = useRef<HTMLDivElement>(null)
  const pinnedRef = useRef(true)

  // Follow the tail only while the user is already at the bottom, so scrolling
  // back to read something does not yank them forward.
  useEffect(() => {
    const box = boxRef.current
    if (box && pinnedRef.current) box.scrollTop = box.scrollHeight
  }, [lines])

  function handleScroll() {
    const box = boxRef.current
    if (!box) return
    pinnedRef.current =
      box.scrollHeight - box.scrollTop - box.clientHeight < 40
  }

  return (
    <div ref={boxRef} className={styles.terminal} onScroll={handleScroll}>
      {lines.length === 0 && <p className={styles.logEmpty}>{empty}</p>}
      {lines.map((line, i) => (
        <div key={i} className={styles.logLine}>
          {/* A stored build log has no per-line timestamps to show. */}
          {showTime && (
            <span className={styles.logTime}>
              {new Date(line.ts).toLocaleTimeString()}
            </span>
          )}
          <span
            className={
              line.source === 'app'
                ? `${styles.logSource} ${styles.logSourceApp}`
                : styles.logSource
            }
          >
            {line.source}
          </span>
          <span className={styles.logText}>{line.text}</span>
        </div>
      ))}
    </div>
  )
}

/** Deploy history, with rollback. */
function DeploysSection({
  appId,
  deploys,
  refresh,
}: {
  appId: string
  deploys: Deploy[]
  refresh: () => Promise<void>
}) {
  const confirm = useConfirm()
  const [rollingBack, setRollingBack] = useState(false)
  const [error, setError] = useState('')

  async function handleRollback(target: Deploy) {
    const confirmed = await confirm({
      title: `Roll back to ${target.id.slice(0, 8)}?`,
      description:
        'The image from that deploy is redeployed. Nothing is rebuilt, so this takes seconds.',
      consequence:
        'Your current environment, replica count and health check are kept — only the image changes. Rolling back does not revert your repository.',
      confirmLabel: 'Roll back',
      destructive: false,
    })
    if (!confirmed) return

    setRollingBack(true)
    setError('')
    try {
      await api.rollback(appId, target.id)
      await refresh()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not roll back.')
    } finally {
      setRollingBack(false)
    }
  }

  return (
    <section className={styles.section}>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Deploys</h2>
      </div>
      <div className={styles.panel}>
        {error && <p className={styles.emptyRow}>{error}</p>}
        {deploys.length === 0 ? (
          <p className={styles.emptyRow}>No deploys yet.</p>
        ) : (
          deploys.map((d, i) => (
            <div key={d.id} className={styles.row}>
              <StatusBadge status={d.status} />
              <span className={styles.rowId}>{d.id.slice(0, 8)}</span>
              {d.rolled_back_from && (
                <span className={styles.rollbackNote}>
                  rollback of {d.rolled_back_from.slice(0, 8)}
                </span>
              )}
              <span className={styles.rowSpacer} />
              <span className={styles.rowTime}>
                {new Date(d.created_at).toLocaleString()}
              </span>
              {/* Only a succeeded deploy that recorded its image can be
                  restored, and restoring the one already running is a no-op. */}
              {d.status === 'succeeded' && d.image && i > 0 && (
                <Button
                  size="sm"
                  onClick={() => void handleRollback(d)}
                  disabled={rollingBack}
                >
                  Roll back
                </Button>
              )}
            </div>
          ))
        )}
      </div>
    </section>
  )
}

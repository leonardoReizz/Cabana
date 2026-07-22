import { useEffect, useMemo, useState } from 'react'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { Link, useNavigate } from 'react-router-dom'
import {
  ApiError,
  api,
  type HealthType,
  type Service,
} from '../api/client'
import { appNameSchema, envKeySchema, repoURLSchema } from '../schemas'
import { Button } from '../ui/Button'
import { Field } from '../ui/Field'
import { fast, normal, slide } from '../ui/motion'
import styles from './NewAppPage.module.css'

/**
 * Guided setup for a new app.
 *
 * The old flow was a three-field dialog followed by hunting through Settings,
 * which is fine until the app needs configuration *before* its first build —
 * a Vite app whose VITE_* variables are inlined at build time gets a broken
 * bundle if it is deployed first and configured after. The wizard exists so
 * everything that affects the first build is asked for before it runs.
 *
 * Nothing is written until the last step: the whole configuration goes to the
 * API in one request, so an abandoned setup leaves nothing behind.
 */

interface Pair {
  key: string
  value: string
}

interface Draft {
  name: string
  repoURL: string
  gitRef: string
  buildArgs: Pair[]
  env: Pair[]
  serviceIDs: string[]
  replicas: number
  healthType: HealthType
  healthPath: string
  initialDelay: number
  period: number
}

const EMPTY: Draft = {
  name: '',
  repoURL: '',
  gitRef: 'main',
  buildArgs: [],
  env: [],
  serviceIDs: [],
  replicas: 1,
  healthType: 'tcp',
  healthPath: '/',
  initialDelay: 3,
  period: 10,
}

const STEPS = ['Source', 'Build', 'Runtime', 'Scale', 'Review']

export function NewAppPage() {
  const navigate = useNavigate()
  const [step, setStep] = useState(0)
  // Which way the last move went, so a step can slide in from the side it came
  // from. Without it, going back would still animate forwards and feel wrong.
  const [direction, setDirection] = useState(1)
  const [furthest, setFurthest] = useState(0)
  const reduceMotion = useReducedMotion()
  const [draft, setDraft] = useState<Draft>(EMPTY)
  const [services, setServices] = useState<Service[]>([])
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [submitError, setSubmitError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    api
      .listServices()
      .then((all) => setServices(all.filter((s) => s.status === 'running')))
      .catch(() => setServices([]))
  }, [])

  function set<K extends keyof Draft>(key: K, value: Draft[K]) {
    setDraft((prev) => ({ ...prev, [key]: value }))
  }

  /** Validates only the step being left, so a user is never shown errors for
   *  fields they have not reached yet. */
  function validateStep(index: number): boolean {
    const next: Record<string, string> = {}

    if (index === 0) {
      const name = appNameSchema.safeParse(draft.name)
      if (!name.success) next.name = name.error.issues[0]?.message ?? 'Invalid'
      const repo = repoURLSchema.safeParse(draft.repoURL)
      if (!repo.success) next.repoURL = repo.error.issues[0]?.message ?? 'Invalid'
    }

    if (index === 1) {
      draft.buildArgs.forEach((p, i) => {
        if (!p.key.trim()) return
        const k = envKeySchema.safeParse(p.key)
        if (!k.success) next[`buildArg${i}`] = k.error.issues[0]?.message ?? 'Invalid'
      })
    }

    if (index === 2) {
      draft.env.forEach((p, i) => {
        if (!p.key.trim()) return
        const k = envKeySchema.safeParse(p.key)
        if (!k.success) next[`env${i}`] = k.error.issues[0]?.message ?? 'Invalid'
      })
    }

    if (index === 3) {
      if (draft.replicas < 1 || draft.replicas > 20) {
        next.replicas = 'Between 1 and 20'
      }
      if (draft.healthType === 'http' && !draft.healthPath.startsWith('/')) {
        next.healthPath = 'Must start with /'
      }
      if (draft.period < 1 || draft.period > 300) next.period = 'Between 1 and 300'
      if (draft.initialDelay < 0 || draft.initialDelay > 600) {
        next.initialDelay = 'Between 0 and 600'
      }
    }

    setErrors(next)
    return Object.keys(next).length === 0
  }

  function goNext() {
    if (!validateStep(step)) return
    const next = Math.min(step + 1, STEPS.length - 1)
    setDirection(1)
    setStep(next)
    setFurthest((f) => Math.max(f, next))
  }

  function goBack() {
    setErrors({})
    setDirection(-1)
    setStep((s) => Math.max(s - 1, 0))
  }

  /** Jumping back to a visited step. Forward jumps stay blocked so validation
   *  is never skipped. */
  function goTo(index: number) {
    if (index > furthest || index === step) return
    setErrors({})
    setDirection(index > step ? 1 : -1)
    setStep(index)
  }

  const pairsToObject = (pairs: Pair[]) =>
    Object.fromEntries(
      pairs.filter((p) => p.key.trim()).map((p) => [p.key.trim(), p.value]),
    )

  async function submit(deployNow: boolean) {
    setSubmitError('')
    setSubmitting(true)
    try {
      const app = await api.createApp({
        name: draft.name,
        repo_url: draft.repoURL,
        git_ref: draft.gitRef || 'main',
        build_args: pairsToObject(draft.buildArgs),
        env: pairsToObject(draft.env),
        service_ids: draft.serviceIDs,
        replicas: draft.replicas,
        health: {
          type: draft.healthType,
          path: draft.healthPath || '/',
          initial_delay_seconds: draft.initialDelay,
          period_seconds: draft.period,
        },
      })
      if (deployNow) {
        // Best-effort: the app exists either way, and its page shows what
        // happened. Failing the whole flow over a deploy that can be retried
        // with one click would be worse.
        await api.deploy(app.id).catch(() => undefined)
      }
      void navigate(`/apps/${app.id}${deployNow ? '/logs' : ''}`)
    } catch (err) {
      setSubmitError(
        err instanceof ApiError ? err.message : 'Could not create the app.',
      )
      // Send them back to the field that is wrong rather than leaving them on
      // a review screen with an error about something they cannot see.
      if (err instanceof ApiError && err.code === 'name_taken') setStep(0)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className={styles.layout}>
      <div className={styles.rail}>
        <div className={styles.railHead}>
          <h1 className={styles.title}>New app</h1>
          <p className={styles.subtitle}>
            Configured before the first build, so the build has what it needs.
          </p>
        </div>

        <ol className={styles.steps}>
          {STEPS.map((label, i) => {
            const visited = i <= furthest
            return (
              <li key={label}>
                <button
                  type="button"
                  onClick={() => goTo(i)}
                  disabled={!visited || i === step}
                  aria-current={i === step ? 'step' : undefined}
                  className={[
                    styles.step,
                    i === step && styles.stepCurrent,
                    i < step && styles.stepDone,
                    visited && i !== step && styles.stepClickable,
                  ]
                    .filter(Boolean)
                    .join(' ')}
                >
                  <span className={styles.marker} aria-hidden="true">
                    {i < step ? '✓' : i + 1}
                  </span>
                  {label}
                </button>
              </li>
            )
          })}
        </ol>
      </div>

      <div className={styles.panel}>
        <div className={styles.stepBody}>
          {/* mode="wait" so the outgoing step finishes before the next arrives —
              overlapping them would show two forms at once. */}
          <AnimatePresence mode="wait" custom={direction} initial={false}>
            <motion.div
              key={step}
              custom={direction}
              variants={reduceMotion ? undefined : slide}
              initial="hidden"
              animate="visible"
              exit="exit"
              transition={normal}
            >
              {step === 0 && <SourceStep draft={draft} set={set} errors={errors} />}
              {step === 1 && <BuildStep draft={draft} set={set} errors={errors} />}
              {step === 2 && (
                <RuntimeStep
                  draft={draft}
                  set={set}
                  errors={errors}
                  services={services}
                />
              )}
              {step === 3 && <ScaleStep draft={draft} set={set} errors={errors} />}
              {step === 4 && <ReviewStep draft={draft} services={services} />}
            </motion.div>
          </AnimatePresence>
        </div>

        {submitError && (
          <p className={styles.error} role="alert">
            {submitError}
          </p>
        )}

        <div className={styles.actions}>
          {step === 0 ? (
            <Link to="/apps">
              <Button variant="ghost" type="button">
                Cancel
              </Button>
            </Link>
          ) : (
            <Button variant="ghost" type="button" onClick={goBack}>
              Back
            </Button>
          )}

          <div className={styles.actionsRight}>
            {step < STEPS.length - 1 ? (
              <Button variant="primary" type="button" onClick={goNext}>
                Continue
              </Button>
            ) : (
              <>
                <Button
                  type="button"
                  onClick={() => void submit(false)}
                  disabled={submitting}
                >
                  Create only
                </Button>
                <Button
                  variant="primary"
                  type="button"
                  onClick={() => void submit(true)}
                  loading={submitting}
                >
                  Create and deploy
                </Button>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

type SetFn = <K extends keyof Draft>(key: K, value: Draft[K]) => void

function SourceStep({
  draft,
  set,
  errors,
}: {
  draft: Draft
  set: SetFn
  errors: Record<string, string>
}) {
  return (
    <>
      <h2 className={styles.stepTitle}>Where does it come from?</h2>
      <p className={styles.stepLede}>
        A public Git repository with a Dockerfile at its root.
      </p>
      <div className={styles.fields}>
        <Field
          label="Name"
          value={draft.name}
          onChange={(e) => set('name', e.target.value.toLowerCase())}
          placeholder="my-app"
          hint="Becomes the subdomain. Lowercase letters, digits and hyphens."
          error={errors.name}
          autoFocus
        />
        <Field
          label="Repository URL"
          value={draft.repoURL}
          onChange={(e) => set('repoURL', e.target.value)}
          placeholder="https://github.com/you/my-app"
          hint="Paste it as your browser shows it."
          mono
          error={errors.repoURL}
        />
        <Field
          label="Branch"
          value={draft.gitRef}
          onChange={(e) => set('gitRef', e.target.value)}
          placeholder="main"
          mono
        />
      </div>
    </>
  )
}

function BuildStep({
  draft,
  set,
  errors,
}: {
  draft: Draft
  set: SetFn
  errors: Record<string, string>
}) {
  return (
    <>
      <h2 className={styles.stepTitle}>Build-time variables</h2>
      <p className={styles.stepLede}>
        Passed to your Dockerfile as <code>--build-arg</code>. Leave empty if
        your app reads its configuration at run time.
      </p>

      <div className={styles.fields}>
        <div className={styles.note}>
          <p>
            <strong>Using Vite, Create React App or a static site generator?</strong>{' '}
            Their variables — <code>VITE_*</code>, <code>REACT_APP_*</code>,{' '}
            <code>NEXT_PUBLIC_*</code> — are inlined into the bundle while it
            builds. They belong here, not in the next step. Your Dockerfile also
            needs a matching <code>ARG</code> line for each one.
          </p>
        </div>

        <PairEditor
          pairs={draft.buildArgs}
          onChange={(pairs) => set('buildArgs', pairs)}
          errors={errors}
          errorPrefix="buildArg"
          keyPlaceholder="VITE_API_URL"
          emptyLabel="No build-time variables."
        />

        {draft.buildArgs.some((p) => p.key.trim()) && (
          <p className={styles.warning}>
            Build args are stored in the image and readable from its history.
            Do not put secrets here — use the next step for those.
          </p>
        )}
      </div>
    </>
  )
}

function RuntimeStep({
  draft,
  set,
  errors,
  services,
}: {
  draft: Draft
  set: SetFn
  errors: Record<string, string>
  services: Service[]
}) {
  function toggleService(id: string) {
    set(
      'serviceIDs',
      draft.serviceIDs.includes(id)
        ? draft.serviceIDs.filter((s) => s !== id)
        : [...draft.serviceIDs, id],
    )
  }

  return (
    <>
      <h2 className={styles.stepTitle}>Run-time environment</h2>
      <p className={styles.stepLede}>
        Read by your process when it starts. Safe for secrets — these are stored
        as Kubernetes Secrets and never baked into the image.
      </p>

      <div className={styles.fields}>
        <PairEditor
          pairs={draft.env}
          onChange={(pairs) => set('env', pairs)}
          errors={errors}
          errorPrefix="env"
          keyPlaceholder="DATABASE_URL"
          emptyLabel="No environment variables."
        />

        {services.length > 0 && (
          <div>
            <p className={styles.stepLede}>
              Link a service and its connection string is injected for you.
            </p>
            <div className={styles.serviceList}>
              {services.map((s) => (
                <label key={s.id} className={styles.serviceOption}>
                  <input
                    type="checkbox"
                    checked={draft.serviceIDs.includes(s.id)}
                    onChange={() => toggleService(s.id)}
                  />
                  {s.name}
                  <span className={styles.serviceMeta}>{s.template}</span>
                </label>
              ))}
            </div>
          </div>
        )}
      </div>
    </>
  )
}

function ScaleStep({
  draft,
  set,
  errors,
}: {
  draft: Draft
  set: SetFn
  errors: Record<string, string>
}) {
  return (
    <>
      <h2 className={styles.stepTitle}>Scale and health</h2>
      <p className={styles.stepLede}>
        The defaults work for most apps. All of this can be changed later.
      </p>

      <div className={styles.grid2}>
        <Field
          label="Replicas"
          type="number"
          min={1}
          max={20}
          value={draft.replicas}
          onChange={(e) => set('replicas', e.target.valueAsNumber)}
          hint="How many copies run at once."
          error={errors.replicas}
        />
        <Field
          label="Start delay (s)"
          type="number"
          min={0}
          max={600}
          value={draft.initialDelay}
          onChange={(e) => set('initialDelay', e.target.valueAsNumber)}
          hint="Raise it for a slow boot."
          error={errors.initialDelay}
        />
        <Field
          label="Check every (s)"
          type="number"
          min={1}
          max={300}
          value={draft.period}
          onChange={(e) => set('period', e.target.valueAsNumber)}
          hint="How often the health check runs."
          error={errors.period}
        />
      </div>

      <div className={styles.fields} style={{ marginTop: 'var(--space-4)' }}>
        <label className={styles.checkRow}>
          <input
            type="checkbox"
            checked={draft.healthType === 'http'}
            onChange={(e) => set('healthType', e.target.checked ? 'http' : 'tcp')}
          />
          Check an HTTP path instead of just the port
        </label>

        {/* Height animates from zero so the panel grows into the extra field
            rather than snapping. */}
        <AnimatePresence initial={false}>
          {draft.healthType === 'http' && (
            <motion.div
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: 'auto' }}
              exit={{ opacity: 0, height: 0 }}
              transition={normal}
              style={{ overflow: 'hidden' }}
            >
              <Field
                label="Health path"
                value={draft.healthPath}
                onChange={(e) => set('healthPath', e.target.value)}
                placeholder="/healthz"
                hint="Must answer 2xx, or replicas never become ready."
                mono
                error={errors.healthPath}
              />
            </motion.div>
          )}
        </AnimatePresence>
      </div>
    </>
  )
}

function ReviewStep({
  draft,
  services,
}: {
  draft: Draft
  services: Service[]
}) {
  const buildArgs = draft.buildArgs.filter((p) => p.key.trim())
  const env = draft.env.filter((p) => p.key.trim())
  const linked = useMemo(
    () => services.filter((s) => draft.serviceIDs.includes(s.id)),
    [services, draft.serviceIDs],
  )

  const row = (label: string, value: React.ReactNode) => (
    <div className={styles.reviewRow}>
      <span className={styles.reviewLabel}>{label}</span>
      <span className={styles.reviewValue}>{value}</span>
    </div>
  )
  const none = <span className={styles.reviewMuted}>none</span>

  return (
    <>
      <h2 className={styles.stepTitle}>Ready to create</h2>
      <p className={styles.stepLede}>
        Everything below is applied before the first build runs.
      </p>

      <div className={styles.review}>
        {row('Name', draft.name)}
        {row('Repository', `${draft.repoURL} · ${draft.gitRef || 'main'}`)}
        {/* Values are deliberately not shown: a review screen is exactly where
            someone screenshots their setup. */}
        {row(
          'Build args',
          buildArgs.length ? buildArgs.map((p) => p.key).join(', ') : none,
        )}
        {row('Environment', env.length ? env.map((p) => p.key).join(', ') : none)}
        {row(
          'Services',
          linked.length ? linked.map((s) => s.name).join(', ') : none,
        )}
        {row('Replicas', String(draft.replicas))}
        {row(
          'Health check',
          draft.healthType === 'http'
            ? `HTTP ${draft.healthPath} every ${draft.period}s, after ${draft.initialDelay}s`
            : `TCP every ${draft.period}s, after ${draft.initialDelay}s`,
        )}
      </div>
    </>
  )
}

function PairEditor({
  pairs,
  onChange,
  errors,
  errorPrefix,
  keyPlaceholder,
  emptyLabel,
}: {
  pairs: Pair[]
  onChange: (pairs: Pair[]) => void
  errors: Record<string, string>
  errorPrefix: string
  keyPlaceholder: string
  emptyLabel: string
}) {
  function update(i: number, patch: Partial<Pair>) {
    onChange(pairs.map((p, j) => (j === i ? { ...p, ...patch } : p)))
  }

  return (
    <div className={styles.pairs}>
      {pairs.length === 0 && <p className={styles.pairEmpty}>{emptyLabel}</p>}

      <AnimatePresence initial={false}>
      {pairs.map((pair, i) => {
        const error = errors[`${errorPrefix}${i}`]
        return (
          <motion.div
            key={i}
            layout
            initial={{ opacity: 0, y: -4 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, x: -8 }}
            transition={fast}
          >
            <div className={styles.pairRow}>
              <input
                className={`${styles.pairInput} ${error ? styles.pairInvalid : ''}`}
                value={pair.key}
                onChange={(e) => update(i, { key: e.target.value })}
                placeholder={keyPlaceholder}
                aria-label={`Variable ${i + 1} name`}
                aria-invalid={error ? true : undefined}
                spellCheck={false}
              />
              <input
                className={styles.pairInput}
                value={pair.value}
                onChange={(e) => update(i, { value: e.target.value })}
                placeholder="value"
                aria-label={`Variable ${i + 1} value`}
                spellCheck={false}
              />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => onChange(pairs.filter((_, j) => j !== i))}
                aria-label={`Remove variable ${i + 1}`}
              >
                ✕
              </Button>
            </div>
            {error && (
              <span className={styles.fieldError} role="alert">
                {error}
              </span>
            )}
          </motion.div>
        )
      })}
      </AnimatePresence>

      <div>
        <Button
          type="button"
          size="sm"
          onClick={() => onChange([...pairs, { key: '', value: '' }])}
        >
          Add variable
        </Button>
      </div>
    </div>
  )
}

import { useCallback, useEffect, useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Link, useOutletContext } from 'react-router-dom'
import {
  ApiError,
  api,
  type App,
  type Service,
  type ServiceLink,
} from '../../api/client'
import {
  envSchema,
  settingsSchema,
  type EnvInput,
  type SettingsInput,
} from '../../schemas'
import { Button } from '../../ui/Button'
import { Field } from '../../ui/Field'
import { Select } from '../../ui/Select'
import type { AppTabContext } from './AppDetailPage'
import styles from './app.module.css'

/** How the app runs. Every change here applies on the next deploy. */
export function SettingsTab() {
  const { app, refresh } = useOutletContext<AppTabContext>()
  return (
    <>
      <SettingsSection app={app} onSaved={refresh} />
      <BuildArgsSection appId={app.id} />
      <EnvSection appId={app.id} />
      <LinksSection appId={app.id} />
    </>
  )
}

/** Replica count and health check. Both apply on the next deploy. */
function SettingsSection({
  app,
  onSaved,
}: {
  app: App
  onSaved: () => Promise<void>
}) {
  const [saved, setSaved] = useState(false)
  const [formError, setFormError] = useState('')

  const {
    register,
    handleSubmit,
    watch,
    formState: { errors, isSubmitting },
  } = useForm<SettingsInput>({
    resolver: zodResolver(settingsSchema),
    defaultValues: {
      replicas: app.replicas,
      healthType: app.health.type,
      healthPath: app.health.path || '/',
      initialDelay: app.health.initial_delay_seconds,
      period: app.health.period_seconds,
    },
  })

  const healthType = watch('healthType')

  async function onSubmit(values: SettingsInput) {
    setFormError('')
    try {
      await api.updateSettings(app.id, {
        replicas: values.replicas,
        health: {
          type: values.healthType,
          path: values.healthPath || '/',
          initial_delay_seconds: values.initialDelay,
          period_seconds: values.period,
        },
      })
      setSaved(true)
      await onSaved()
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Could not save.')
    }
  }

  return (
    <section className={styles.section}>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Scale and health</h2>
      </div>
      <form className={styles.panel} onSubmit={handleSubmit(onSubmit)} noValidate>
        <div className={styles.settingsGrid}>
          <Field
            label="Replicas"
            type="number"
            min={1}
            max={20}
            hint="How many copies run at once."
            error={errors.replicas?.message}
            {...register('replicas', {
              valueAsNumber: true,
              onChange: () => setSaved(false),
            })}
          />

          <div className={styles.radioGroup}>
            <span className={styles.radioLabel}>Health check</span>
            <div className={styles.radioRow}>
              <label className={styles.radioOption}>
                <input
                  type="radio"
                  value="tcp"
                  {...register('healthType', { onChange: () => setSaved(false) })}
                />
                TCP
              </label>
              <label className={styles.radioOption}>
                <input
                  type="radio"
                  value="http"
                  {...register('healthType', { onChange: () => setSaved(false) })}
                />
                HTTP
              </label>
            </div>
          </div>

          {/* Only meaningful for HTTP, so it is not shown for TCP at all. */}
          {healthType === 'http' && (
            <Field
              label="Health path"
              placeholder="/healthz"
              hint="Must answer 2xx, or the replica never becomes ready."
              mono
              error={errors.healthPath?.message}
              {...register('healthPath', { onChange: () => setSaved(false) })}
            />
          )}

          <Field
            label="Start delay (s)"
            type="number"
            min={0}
            max={600}
            hint="Grace period before the first check. Raise it for slow boots."
            error={errors.initialDelay?.message}
            {...register('initialDelay', {
              valueAsNumber: true,
              onChange: () => setSaved(false),
            })}
          />

          <Field
            label="Check every (s)"
            type="number"
            min={1}
            max={300}
            hint="Three failures in a row pull a replica, so this sets how fast a fault is noticed."
            error={errors.period?.message}
            {...register('period', {
              valueAsNumber: true,
              onChange: () => setSaved(false),
            })}
          />
        </div>

        <div className={styles.envFooter}>
          <span className={styles.envNote}>
            {formError
              ? formError
              : saved
                ? 'Saved — redeploy to apply.'
                : 'Changes apply on the next deploy.'}
          </span>
          <Button type="submit" variant="primary" size="sm" loading={isSubmitting}>
            Save
          </Button>
        </div>
      </form>
    </section>
  )
}

function EnvSection({ appId }: { appId: string }) {
  const [saved, setSaved] = useState(false)
  const [formError, setFormError] = useState('')

  const {
    control,
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<EnvInput>({
    resolver: zodResolver(envSchema),
    defaultValues: { entries: [] },
  })

  const { fields, append, remove } = useFieldArray({ control, name: 'entries' })

  useEffect(() => {
    api
      .getEnv(appId)
      .then((env) =>
        reset({
          entries: Object.entries(env).map(([key, value]) => ({ key, value })),
        }),
      )
      .catch(() => setFormError('Could not load environment variables.'))
  }, [appId, reset])

  async function onSubmit(values: EnvInput) {
    setFormError('')
    try {
      // A blank key is how a user abandons a row they added by mistake, so drop
      // those rather than rejecting the whole save.
      const env = Object.fromEntries(
        values.entries
          .filter((e) => e.key.trim())
          .map((e) => [e.key.trim(), e.value]),
      )
      await api.putEnv(appId, env)
      setSaved(true)
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Could not save.')
    }
  }

  return (
    <section className={styles.section}>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Environment</h2>
      </div>
      <form className={styles.panel} onSubmit={handleSubmit(onSubmit)} noValidate>
        <div className={styles.envList}>
          {fields.length === 0 && (
            <p className={styles.emptyRow}>No environment variables set.</p>
          )}
          {fields.map((field, i) => (
            <div key={field.id} className={styles.envRow}>
              <div className={styles.envCell}>
                <input
                  className={`${styles.envInput} ${
                    errors.entries?.[i]?.key ? styles.envInvalid : ''
                  }`}
                  placeholder="KEY"
                  aria-label={`Variable ${i + 1} name`}
                  aria-invalid={errors.entries?.[i]?.key ? true : undefined}
                  spellCheck={false}
                  {...register(`entries.${i}.key`, {
                    onChange: () => setSaved(false),
                  })}
                />
                {errors.entries?.[i]?.key && (
                  <span className={styles.envError} role="alert">
                    {errors.entries[i]?.key?.message}
                  </span>
                )}
              </div>
              <input
                className={styles.envInput}
                placeholder="value"
                aria-label={`Variable ${i + 1} value`}
                spellCheck={false}
                {...register(`entries.${i}.value`, {
                  onChange: () => setSaved(false),
                })}
              />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  remove(i)
                  setSaved(false)
                }}
                aria-label={`Remove variable ${i + 1}`}
              >
                ✕
              </Button>
            </div>
          ))}
        </div>

        <div className={styles.envFooter}>
          <Button
            type="button"
            size="sm"
            onClick={() => append({ key: '', value: '' })}
          >
            Add variable
          </Button>
          <span className={styles.envNote}>
            {formError
              ? formError
              : saved
                ? 'Saved — redeploy to apply.'
                : 'Changes apply on the next deploy.'}
          </span>
          <Button
            type="submit"
            variant="primary"
            size="sm"
            loading={isSubmitting}
          >
            Save
          </Button>
        </div>
      </form>
    </section>
  )
}


/**
 * Services wired into this app.
 *
 * The injected variable is deliberately not editable here: it is rendered from
 * the service's live credentials at deploy time, so there is nothing to edit
 * and nothing that can drift.
 */
function LinksSection({ appId }: { appId: string }) {
  const [links, setLinks] = useState<ServiceLink[]>([])
  const [services, setServices] = useState<Service[]>([])
  const [selected, setSelected] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const refresh = useCallback(async () => {
    try {
      const [ls, svcs] = await Promise.all([
        api.listLinks(appId),
        api.listServices(),
      ])
      setLinks(ls)
      setServices(svcs)
    } catch {
      setError('Could not load linked services.')
    }
  }, [appId])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const linkedIds = new Set(links.map((l) => l.service_id))
  // Only running services can be linked: a provisioning one has no credentials
  // to render a connection string from yet.
  const available = services.filter(
    (s) => !linkedIds.has(s.id) && s.status === 'running',
  )

  async function link() {
    if (!selected) return
    setBusy(true)
    setError('')
    try {
      const res = await api.linkService(appId, selected)
      setNotice(`${res.env_var} will be injected on the next deploy.`)
      setSelected('')
      await refresh()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not link.')
    } finally {
      setBusy(false)
    }
  }

  async function unlink(serviceId: string, envVar: string) {
    setError('')
    try {
      await api.unlinkService(appId, serviceId)
      setNotice(`${envVar} will be removed on the next deploy.`)
      await refresh()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not unlink.')
    }
  }

  return (
    <section className={styles.section}>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Services</h2>
      </div>
      <div className={styles.panel}>
        {links.length === 0 && (
          <p className={styles.emptyRow}>
            No services linked.{' '}
            <Link to="/services">Install one from the catalogue</Link> and its
            connection string is injected for you.
          </p>
        )}
        {links.map((l) => (
          <div key={l.service_id} className={styles.row}>
            <Link to={`/services/${l.service_id}`}>{l.service_name}</Link>
            <span className={styles.rowId}>{l.env_var}</span>
            <span className={styles.rowSpacer} />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => void unlink(l.service_id, l.env_var)}
              aria-label={`Unlink ${l.service_name}`}
            >
              Unlink
            </Button>
          </div>
        ))}

        <div className={styles.envFooter}>
          <Select
            value={selected}
            onChange={setSelected}
            ariaLabel="Service to link"
            disabled={available.length === 0}
            placeholder={
              available.length === 0
                ? 'No services available'
                : 'Choose a service…'
            }
            options={available.map((s) => ({
              value: s.id,
              label: `${s.name} (${s.template})`,
            }))}
          />
          <span className={styles.envNote}>{error || notice}</span>
          <Button
            variant="primary"
            size="sm"
            onClick={() => void link()}
            loading={busy}
            disabled={!selected}
          >
            Link
          </Button>
        </div>
      </div>
    </section>
  )
}


/**
 * Build-time variables.
 *
 * Separate from Environment on purpose: these are baked into the image and only
 * change on a rebuild, which is the whole reason a Vite app cannot be
 * configured through runtime env.
 */
function BuildArgsSection({ appId }: { appId: string }) {
  const [saved, setSaved] = useState(false)
  const [formError, setFormError] = useState('')

  const {
    control,
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<EnvInput>({
    resolver: zodResolver(envSchema),
    defaultValues: { entries: [] },
  })

  const { fields, append, remove } = useFieldArray({ control, name: 'entries' })

  useEffect(() => {
    api
      .getBuildArgs(appId)
      .then((args) =>
        reset({
          entries: Object.entries(args).map(([key, value]) => ({ key, value })),
        }),
      )
      .catch(() => setFormError('Could not load build args.'))
  }, [appId, reset])

  async function onSubmit(values: EnvInput) {
    setFormError('')
    try {
      const args = Object.fromEntries(
        values.entries
          .filter((e) => e.key.trim())
          .map((e) => [e.key.trim(), e.value]),
      )
      await api.putBuildArgs(appId, args)
      setSaved(true)
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Could not save.')
    }
  }

  return (
    <section className={styles.section}>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Build arguments</h2>
      </div>
      <form className={styles.panel} onSubmit={handleSubmit(onSubmit)} noValidate>
        <div className={styles.envList}>
          {fields.length === 0 && (
            <p className={styles.emptyRow}>
              No build arguments. Vite, CRA and static site generators inline
              their variables here, not in Environment.
            </p>
          )}
          {fields.map((field, i) => (
            <div key={field.id} className={styles.envRow}>
              <div className={styles.envCell}>
                <input
                  className={`${styles.envInput} ${
                    errors.entries?.[i]?.key ? styles.envInvalid : ''
                  }`}
                  placeholder="VITE_API_URL"
                  aria-label={`Build argument ${i + 1} name`}
                  aria-invalid={errors.entries?.[i]?.key ? true : undefined}
                  spellCheck={false}
                  {...register(`entries.${i}.key`, {
                    onChange: () => setSaved(false),
                  })}
                />
                {errors.entries?.[i]?.key && (
                  <span className={styles.envError} role="alert">
                    {errors.entries[i]?.key?.message}
                  </span>
                )}
              </div>
              <input
                className={styles.envInput}
                placeholder="value"
                aria-label={`Build argument ${i + 1} value`}
                spellCheck={false}
                {...register(`entries.${i}.value`, {
                  onChange: () => setSaved(false),
                })}
              />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  remove(i)
                  setSaved(false)
                }}
                aria-label={`Remove build argument ${i + 1}`}
              >
                ✕
              </Button>
            </div>
          ))}
        </div>

        <div className={styles.envFooter}>
          <Button
            type="button"
            size="sm"
            onClick={() => append({ key: '', value: '' })}
          >
            Add argument
          </Button>
          <span className={styles.envNote}>
            {formError
              ? formError
              : saved
                ? 'Saved — redeploy to rebuild with these.'
                : 'Baked into the image on the next deploy. Not for secrets.'}
          </span>
          <Button
            type="submit"
            variant="primary"
            size="sm"
            loading={isSubmitting}
          >
            Save
          </Button>
        </div>
      </form>
    </section>
  )
}

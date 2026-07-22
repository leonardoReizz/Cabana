import { useCallback, useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { motion } from 'motion/react'
import { Link } from 'react-router-dom'
import {
  ApiError,
  api,
  type CatalogEntry,
  type Service,
} from '../api/client'
import { createServiceSchema, type CreateServiceInput } from '../schemas'
import { Button } from '../ui/Button'
import { Field } from '../ui/Field'
import { StatusBadge } from '../ui/StatusBadge'
import { listContainer, listItem } from '../ui/motion'
import styles from './ServicesPage.module.css'

const PROVISIONING = 'provisioning'

export function ServicesPage() {
  const [templates, setTemplates] = useState<CatalogEntry[]>([])
  const [services, setServices] = useState<Service[] | null>(null)
  const [error, setError] = useState('')
  const [installing, setInstalling] = useState<CatalogEntry | null>(null)
  const dialogRef = useRef<HTMLDialogElement>(null)

  const refresh = useCallback(async () => {
    try {
      setServices(await api.listServices())
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not load services.')
    }
  }, [])

  useEffect(() => {
    api
      .catalog()
      .then(setTemplates)
      .catch(() => setError('Could not load the catalogue.'))
    void refresh()
  }, [refresh])

  // Poll only while something is still coming up, so an idle dashboard is silent.
  useEffect(() => {
    if (!services?.some((s) => s.status === PROVISIONING)) return
    const timer = setInterval(() => void refresh(), 2000)
    return () => clearInterval(timer)
  }, [services, refresh])

  function openInstall(template: CatalogEntry) {
    setInstalling(template)
    dialogRef.current?.showModal()
  }

  if (services === null && !error) return null

  return (
    <>
      <div className={styles.head}>
        <h1 className={styles.title}>Services</h1>
        <p className={styles.subtitle}>
          Databases and queues your apps can use. Install one, link it to an app,
          and the connection string is injected for you.
        </p>
      </div>

      {error && <p className={styles.error}>{error}</p>}

      {services && services.length > 0 && (
        <>
          <h2 className={styles.sectionTitle}>Installed</h2>
          <motion.div
            className={styles.grid}
            variants={listContainer}
            initial="hidden"
            animate="visible"
          >
            {services.map((svc) => (
              <motion.div key={svc.id} variants={listItem}>
              <Link to={`/services/${svc.id}`} className={styles.card}>
                <div className={styles.cardHead}>
                  <span className={styles.name}>{svc.name}</span>
                  <StatusBadge status={svc.status} />
                </div>
                <p className={styles.description}>
                  {templates.find((t) => t.name === svc.template)
                    ?.display_name ?? svc.template}
                </p>
                <span className={styles.cardMeta}>{svc.image}</span>
                <span className={styles.cardMeta}>
                  {svc.host}:{svc.port}
                </span>
              </Link>
              </motion.div>
            ))}
          </motion.div>
        </>
      )}

      <h2 className={styles.sectionTitle}>Catalogue</h2>
      <motion.div
        className={styles.grid}
        variants={listContainer}
        initial="hidden"
        animate="visible"
      >
        {templates.map((t) => (
          <motion.div key={t.name} variants={listItem} className={styles.card}>
            <div className={styles.cardHead}>
              <span className={styles.name}>{t.display_name}</span>
              <span className={styles.category}>{t.category}</span>
            </div>
            <p className={styles.description}>{t.description}</p>
            <span className={styles.cardMeta}>{t.image}</span>
            <div className={styles.cardFoot}>
              <span className={styles.cardMeta}>{t.env_var}</span>
              <Button
                variant="primary"
                size="sm"
                onClick={() => openInstall(t)}
              >
                Install
              </Button>
            </div>
          </motion.div>
        ))}
      </motion.div>

      <InstallDialog
        ref={dialogRef}
        template={installing}
        onInstalled={refresh}
      />
    </>
  )
}

function InstallDialog({
  ref,
  template,
  onInstalled,
}: {
  ref: React.RefObject<HTMLDialogElement | null>
  template: CatalogEntry | null
  onInstalled: () => Promise<void>
}) {
  const [formError, setFormError] = useState('')

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<CreateServiceInput>({
    resolver: zodResolver(createServiceSchema),
    defaultValues: { name: '', version: '' },
  })

  // Suggest the template's own name, which is what most people want for their
  // first instance of anything.
  useEffect(() => {
    if (template) reset({ name: template.name, version: '' })
  }, [template, reset])

  function close() {
    ref.current?.close()
    setFormError('')
  }

  async function onSubmit(values: CreateServiceInput) {
    if (!template) return
    setFormError('')
    try {
      await api.createService({
        name: values.name,
        template: template.name,
        version: values.version.trim(),
      })
      await onInstalled()
      close()
    } catch (err) {
      setFormError(
        err instanceof ApiError ? err.message : 'Could not install the service.',
      )
    }
  }

  return (
    <dialog ref={ref} className={styles.dialog} onClose={close}>
      <form
        className={styles.dialogForm}
        onSubmit={handleSubmit(onSubmit)}
        noValidate
      >
        <h2 className={styles.dialogTitle}>
          Install {template?.display_name ?? 'service'}
        </h2>

        {formError && (
          <div className={styles.error} role="alert">
            {formError}
          </div>
        )}

        <Field
          label="Name"
          hint="Identifies this instance. You can install more than one."
          error={errors.name?.message}
          {...register('name')}
        />

        <Field
          label="Version"
          placeholder={template?.default_version ?? ''}
          hint={
            template
              ? `Any tag published for ${template.name} on Docker Hub. Leave empty for ${template.default_version}.`
              : undefined
          }
          mono
          error={errors.version?.message}
          {...register('version')}
        />

        <p className={styles.dialogNote}>
          Credentials are generated automatically and shown on the service page.
          {template && ` Linked apps receive ${template.env_var}.`}
        </p>

        <div className={styles.dialogActions}>
          <Button type="button" variant="ghost" onClick={close}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={isSubmitting}>
            Install
          </Button>
        </div>
      </form>
    </dialog>
  )
}

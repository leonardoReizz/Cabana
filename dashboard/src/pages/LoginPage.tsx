import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useAuth } from '../auth'
import { ApiError } from '../api/client'
import { loginSchema, type LoginInput } from '../schemas'
import { Button } from '../ui/Button'
import { Field } from '../ui/Field'
import { Logo } from '../ui/Logo'
import styles from './LoginPage.module.css'

export function LoginPage() {
  const { login } = useAuth()
  const [formError, setFormError] = useState('')

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginInput>({
    resolver: zodResolver(loginSchema),
    defaultValues: { username: 'admin', password: '' },
  })

  async function onSubmit(values: LoginInput) {
    setFormError('')
    try {
      await login(values.username, values.password)
    } catch (err) {
      // Field-level errors come from zod; this is for everything the server
      // rejects that the client could not have known.
      setFormError(
        err instanceof ApiError ? err.message : 'Could not reach the server.',
      )
    }
  }

  return (
    <div className={styles.page}>
      <div className={styles.card}>
        <div className={styles.brand}>
          <Logo size={28} />
          cabana
        </div>
        <p className={styles.tagline}>Your own platform, on your own machine.</p>

        <form className={styles.form} onSubmit={handleSubmit(onSubmit)} noValidate>
          {formError && (
            <div className={styles.error} role="alert">
              {formError}
            </div>
          )}

          <Field
            label="Username"
            autoComplete="username"
            autoFocus
            error={errors.username?.message}
            {...register('username')}
          />
          <Field
            label="Password"
            type="password"
            autoComplete="current-password"
            error={errors.password?.message}
            {...register('password')}
          />

          <Button type="submit" variant="primary" fullWidth loading={isSubmitting}>
            Sign in
          </Button>
        </form>

        <p className={styles.footnote}>
          The initial password was printed by the installer.
        </p>
      </div>
    </div>
  )
}

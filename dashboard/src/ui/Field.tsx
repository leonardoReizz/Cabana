import { useId } from 'react'
import type { InputHTMLAttributes, Ref } from 'react'
import styles from './Field.module.css'

interface Props extends InputHTMLAttributes<HTMLInputElement> {
  label: string
  hint?: string
  error?: string
  mono?: boolean
  // Declared explicitly so react-hook-form's register() — which returns a ref
  // alongside name/onChange/onBlur — can be spread straight onto this component.
  ref?: Ref<HTMLInputElement>
}

export function Field({ label, hint, error, mono, ref, ...rest }: Props) {
  const id = useId()
  const hintId = `${id}-hint`
  const errorId = `${id}-error`

  const classes = [styles.input, mono && styles.mono, error && styles.invalid]
    .filter(Boolean)
    .join(' ')

  return (
    <div className={styles.field}>
      <label className={styles.label} htmlFor={id}>
        {label}
      </label>
      <input
        id={id}
        ref={ref}
        className={classes}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? errorId : hint ? hintId : undefined}
        {...rest}
      />
      {error ? (
        <span id={errorId} className={styles.error} role="alert">
          {error}
        </span>
      ) : hint ? (
        <span id={hintId} className={styles.hint}>
          {hint}
        </span>
      ) : null}
    </div>
  )
}

import { createContext, useCallback, useContext, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import * as AlertDialog from '@radix-ui/react-alert-dialog'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { Button } from './Button'
import { fast, normal } from './motion'
import styles from './AlertDialog.module.css'

/**
 * Confirmation dialogs, built on Radix's AlertDialog primitive.
 *
 * Radix supplies only behaviour — focus trapping, Escape to cancel, the correct
 * `role="alertdialog"` and aria wiring, restoring focus to the trigger on close.
 * Every visual decision is ours. This is the same split shadcn uses, and it is
 * why cabana can keep its own look without reimplementing accessibility.
 *
 * Replaces window.confirm(), which cannot be styled, blocks the main thread,
 * and in some browsers can be suppressed entirely by the user — a destructive
 * action silently losing its confirmation is not an acceptable failure mode.
 */

export interface ConfirmOptions {
  title: string
  description?: string
  /** Rendered in a danger panel: what cannot be undone. */
  consequence?: string
  confirmLabel?: string
  cancelLabel?: string
  /** Styles the confirm button as destructive. Defaults to true. */
  destructive?: boolean
}

type Confirm = (options: ConfirmOptions) => Promise<boolean>

const ConfirmContext = createContext<Confirm | null>(null)

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [options, setOptions] = useState<ConfirmOptions | null>(null)
  // Held in a ref rather than state: resolving must not depend on a re-render
  // having happened.
  const resolveRef = useRef<((confirmed: boolean) => void) | null>(null)
  const reduceMotion = useReducedMotion()

  const confirm = useCallback<Confirm>((opts) => {
    setOptions(opts)
    return new Promise<boolean>((resolve) => {
      resolveRef.current = resolve
    })
  }, [])

  function settle(confirmed: boolean) {
    resolveRef.current?.(confirmed)
    resolveRef.current = null
    setOptions(null)
  }

  const open = options !== null

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}

      <AlertDialog.Root
        open={open}
        // Fires for Escape and overlay dismissal as well as the buttons, so a
        // dismissed dialog resolves false instead of leaving the caller
        // awaiting a promise forever.
        onOpenChange={(next) => {
          if (!next) settle(false)
        }}
      >
        {/* forceMount hands mounting to AnimatePresence, which is what makes an
            exit animation possible — Radix would otherwise unmount the content
            immediately on close and there would be nothing left to animate. */}
        <AnimatePresence>
          {open && (
            <AlertDialog.Portal forceMount>
              <AlertDialog.Overlay asChild forceMount>
                <motion.div
                  className={styles.overlay}
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  exit={{ opacity: 0 }}
                  transition={fast}
                />
              </AlertDialog.Overlay>

              <AlertDialog.Content asChild forceMount>
                <motion.div
                  className={styles.content}
                  initial={reduceMotion ? { opacity: 0 } : { opacity: 0, scale: 0.97, y: 8 }}
                  animate={{ opacity: 1, scale: 1, y: 0 }}
                  exit={reduceMotion ? { opacity: 0 } : { opacity: 0, scale: 0.98, y: 4 }}
                  transition={normal}
                >
                  <AlertDialog.Title className={styles.title}>
                    {options?.title}
                  </AlertDialog.Title>

                  <AlertDialog.Description className={styles.description}>
                    {options?.description}
                    {options?.consequence && (
                      <span className={styles.consequence}>
                        {options.consequence}
                      </span>
                    )}
                  </AlertDialog.Description>

                  <div className={styles.actions}>
                    <AlertDialog.Cancel asChild>
                      <Button variant="ghost">
                        {options?.cancelLabel ?? 'Cancel'}
                      </Button>
                    </AlertDialog.Cancel>
                    <AlertDialog.Action asChild>
                      <Button
                        variant={
                          options?.destructive === false ? 'primary' : 'danger'
                        }
                        onClick={() => settle(true)}
                      >
                        {options?.confirmLabel ?? 'Confirm'}
                      </Button>
                    </AlertDialog.Action>
                  </div>
                </motion.div>
              </AlertDialog.Content>
            </AlertDialog.Portal>
          )}
        </AnimatePresence>
      </AlertDialog.Root>
    </ConfirmContext.Provider>
  )
}

/**
 * Returns an async confirm(). Awaiting it reads like the browser's own:
 *
 *   if (!(await confirm({ title: 'Delete this?' }))) return
 */
export function useConfirm(): Confirm {
  const confirm = useContext(ConfirmContext)
  if (!confirm) {
    throw new Error('useConfirm must be used inside <ConfirmProvider>')
  }
  return confirm
}

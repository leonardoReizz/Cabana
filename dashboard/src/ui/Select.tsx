import * as RadixSelect from '@radix-ui/react-select'
import styles from './Select.module.css'

/**
 * Select built on Radix's primitive.
 *
 * A native <select> cannot be styled consistently across platforms — its menu is
 * drawn by the OS. Radix renders the menu itself while keeping the behaviour a
 * native control has: type-ahead, arrow keys, Home/End, Escape, focus return,
 * and correct listbox aria roles. We supply only the appearance.
 */

export interface SelectOption {
  value: string
  label: string
  disabled?: boolean
}

interface Props {
  value: string
  onChange: (value: string) => void
  options: SelectOption[]
  placeholder?: string
  disabled?: boolean
  /** Required: the control has no visible label of its own. */
  ariaLabel: string
  className?: string
}

export function Select({
  value,
  onChange,
  options,
  placeholder = 'Choose…',
  disabled,
  ariaLabel,
  className,
}: Props) {
  return (
    <RadixSelect.Root value={value} onValueChange={onChange} disabled={disabled}>
      <RadixSelect.Trigger
        className={[styles.trigger, className].filter(Boolean).join(' ')}
        aria-label={ariaLabel}
      >
        <RadixSelect.Value placeholder={placeholder} />
        <RadixSelect.Icon className={styles.icon}>▾</RadixSelect.Icon>
      </RadixSelect.Trigger>

      <RadixSelect.Portal>
        <RadixSelect.Content
          className={styles.content}
          // Popper rather than the default item-aligned position: aligned mode
          // can cover the trigger, which is disorienting when the control sits
          // in a toolbar next to other actions.
          position="popper"
          sideOffset={4}
        >
          <RadixSelect.ScrollUpButton className={styles.scrollButton}>
            ▴
          </RadixSelect.ScrollUpButton>

          <RadixSelect.Viewport className={styles.viewport}>
            {options.map((option) => (
              <RadixSelect.Item
                key={option.value}
                value={option.value}
                disabled={option.disabled}
                className={styles.item}
              >
                {/* The wrapper always occupies the indicator's width, so labels
                    do not shift as the selection moves between items. */}
                <span className={styles.indicator}>
                  <RadixSelect.ItemIndicator>✓</RadixSelect.ItemIndicator>
                </span>
                <RadixSelect.ItemText>{option.label}</RadixSelect.ItemText>
              </RadixSelect.Item>
            ))}
          </RadixSelect.Viewport>

          <RadixSelect.ScrollDownButton className={styles.scrollButton}>
            ▾
          </RadixSelect.ScrollDownButton>
        </RadixSelect.Content>
      </RadixSelect.Portal>
    </RadixSelect.Root>
  )
}

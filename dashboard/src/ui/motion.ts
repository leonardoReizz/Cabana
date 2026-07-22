import type { Transition, Variants } from 'motion/react'

/**
 * Shared motion vocabulary.
 *
 * Animation here is a system, like the colour tokens: a small set of named
 * movements every component draws from, so the app moves consistently instead
 * of each screen inventing its own timing.
 *
 * The rules it encodes:
 *   - Short. Nothing over 300ms; a dashboard is a tool, not a presentation.
 *   - Eased out. Movement decelerates into place, never bounces.
 *   - Small distances. 4–12px. Motion should suggest direction, not travel.
 *   - Opacity plus transform only — never width, height or top/left, which
 *     force layout on every frame.
 */

/** Matches --ease-out in tokens.css so CSS and JS animation agree. */
export const easeOut = [0.2, 0, 0, 1] as const

export const fast: Transition = { duration: 0.12, ease: easeOut }
export const normal: Transition = { duration: 0.2, ease: easeOut }
export const slow: Transition = { duration: 0.3, ease: easeOut }

/** Content arriving in place: fade with a small lift. */
export const rise: Variants = {
  hidden: { opacity: 0, y: 8 },
  visible: { opacity: 1, y: 0 },
  exit: { opacity: 0, y: -8 },
}

/** Plain fade, for anything where movement would be noise. */
export const fade: Variants = {
  hidden: { opacity: 0 },
  visible: { opacity: 1 },
  exit: { opacity: 0 },
}

/**
 * Wizard steps, which move along the axis of travel: forward slides in from the
 * right, back from the left. The direction is what makes a multi-step form feel
 * like one continuous thing rather than a set of unrelated screens.
 */
export const slide: Variants = {
  hidden: (direction: number) => ({ opacity: 0, x: direction > 0 ? 24 : -24 }),
  visible: { opacity: 1, x: 0 },
  exit: (direction: number) => ({ opacity: 0, x: direction > 0 ? -24 : 24 }),
}

/**
 * Staggered lists.
 *
 * The delay is deliberately tiny: at 30ms a grid of cards reads as one group
 * settling, while anything slower turns a page load into a queue the user
 * watches. Capped by delayChildren so a long list does not animate for seconds.
 */
export const listContainer: Variants = {
  hidden: {},
  visible: { transition: { staggerChildren: 0.03, delayChildren: 0.02 } },
}

export const listItem: Variants = {
  hidden: { opacity: 0, y: 6 },
  visible: { opacity: 1, y: 0, transition: normal },
}

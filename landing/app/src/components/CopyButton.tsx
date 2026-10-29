import { cn, useCopyToClipboard } from '@gabvdl/ui'
import { Check, Copy } from 'lucide-react'

import { useCopy } from '../copy/context'

interface Props {
  /** The text to copy; a function is called on click. */
  value: string | (() => string)
  label?: string
  className?: string
}

/**
 * @gabvdl/ui's CopyButton (same classes, same hook) with its "Copy" /
 * "Copied" in the page's language: the design system's are English only.
 */
export function CopyButton({ value, label, className }: Props) {
  const t = useCopy().copy
  const { copy, copied } = useCopyToClipboard()
  const state = copied ? t.copied : t.copy
  return (
    <button
      type="button"
      onClick={() => copy(typeof value === 'function' ? value() : value)}
      aria-label={label ?? state}
      title={state}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-lg px-2 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground',
        copied && 'text-emerald-600 dark:text-emerald-400',
        className,
      )}
    >
      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      {label && <span>{copied ? t.copied : label}</span>}
    </button>
  )
}

import { Tooltip } from '@base-ui/react/tooltip'
import { useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from 'react'
import { useT } from '../lib/i18n'
import { LanguagePicker } from './langpicker'

/** Supplemental help is available on hover, keyboard focus, and tap. */
export function HelpTip({ children, label }: { children: ReactNode; label: string }) {
  const [open, setOpen] = useState(false)
  return <Tooltip.Provider delay={150}>
    <Tooltip.Root open={open} onOpenChange={setOpen}>
      <Tooltip.Trigger type="button" aria-label={label} onClick={() => setOpen(!open)} className="inline-flex size-4 shrink-0 items-center justify-center rounded-full border border-current text-[10px] leading-none text-muted hover:text-fg focus-visible:outline-2 focus-visible:outline-accent">?</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Positioner sideOffset={6} className="z-50">
          <Tooltip.Popup className="max-w-[min(20rem,calc(100vw-2rem))] border border-line bg-raised px-3 py-2 text-xs leading-relaxed text-fg shadow-sm">{children}</Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  </Tooltip.Provider>
}

export function cx(...parts: (string | false | null | undefined)[]) {
  return parts.filter(Boolean).join(' ')
}

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'ghost' | 'danger' }

export function Button({ variant = 'primary', className, ...props }: ButtonProps) {
  return (
    <button
      {...props}
      className={cx(
        'inline-flex h-9 items-center justify-center gap-2 border px-4 text-sm transition-colors',
        'disabled:cursor-not-allowed disabled:opacity-50',
        variant === 'primary' && 'border-accent bg-accent font-medium text-accent-fg hover:bg-transparent hover:text-accent',
        variant === 'ghost' && 'border-line text-muted hover:border-line-strong hover:text-fg',
        variant === 'danger' && 'border-danger/60 text-danger hover:border-danger hover:bg-danger hover:text-bg',
        className,
      )}
    />
  )
}

type FieldProps = InputHTMLAttributes<HTMLInputElement> & { label: string; hint?: string; hintTooltip?: boolean }

export function Field({ label, hint, hintTooltip, id, className, ...props }: FieldProps) {
  const inputId = id ?? props.name
  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-1.5 text-xs text-muted">
        <label htmlFor={inputId}>{label}</label>
        {hint && hintTooltip && <HelpTip label={label}>{hint}</HelpTip>}
      </div>
      <input
        id={inputId}
        {...props}
        className={cx(
          'block h-9 w-full border border-line bg-bg px-3 text-sm text-fg placeholder:text-muted/60',
          'focus:border-accent focus:outline-none',
          className,
        )}
      />
      {hint && !hintTooltip && <p className="text-xs text-muted">{hint}</p>}
    </div>
  )
}

export function Badge({ children, tone = 'accent' }: { children: ReactNode; tone?: 'accent' | 'muted' | 'warn' | 'danger' }) {
  return (
    <span
      className={cx(
        'inline-block border px-1.5 py-0.5 font-pixel text-[10px] uppercase leading-none',
        tone === 'accent' && 'border-accent text-accent',
        tone === 'muted' && 'border-line-strong text-muted',
        tone === 'warn' && 'border-warn text-warn',
        tone === 'danger' && 'border-danger text-danger',
      )}
    >
      {children}
    </span>
  )
}

export function ErrorNote({ children }: { children: ReactNode }) {
  return (
    <p role="alert" className="border border-danger/60 px-3 py-2 text-xs text-danger">
      {children}
    </p>
  )
}

/** Centered card used by the setup and login screens. */
export function AuthCard(props: { product: string; title: string; subtitle: string; children: ReactNode }) {
  return (
    <main className="flex min-h-full items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center justify-between">
          <p className="font-pixel text-xl text-accent">{props.product}</p>
          <LanguagePicker />
        </div>
        <div className="border border-line bg-surface p-6">
          <h1 className="font-pixel text-sm uppercase">{props.title}</h1>
          <p className="mt-1 mb-6 text-xs text-muted">{props.subtitle}</p>
          {props.children}
        </div>
      </div>
    </main>
  )
}

/** Page title with an optional breadcrumb trail above it and actions on the right. */
export function PageHeader(props: { title: ReactNode; crumbs?: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
      <div className="min-w-0">
        {props.crumbs && <p className="mb-1 text-xs text-muted">{props.crumbs}</p>}
        <h1 className="flex flex-wrap items-center gap-3 font-pixel text-base [overflow-wrap:anywhere] uppercase">{props.title}</h1>
        {props.subtitle && <p className="mt-1 text-sm text-muted">{props.subtitle}</p>}
      </div>
      {props.actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{props.actions}</div>}
    </div>
  )
}

/** Bordered section. */
export function Panel(props: { title?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={cx('border border-line bg-surface', props.className)}>
      {(props.title || props.actions) && (
        <div className="flex min-h-11 flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2">
          <h2 className="font-pixel text-xs text-muted uppercase">{props.title}</h2>
          {props.actions}
        </div>
      )}
      {props.children}
    </section>
  )
}

/** What a list shows when it has nothing in it yet. */
export function Empty(props: { title: string; children?: ReactNode }) {
  return (
    <div className="px-4 py-10 text-center">
      <p className="text-sm">{props.title}</p>
      {props.children && <div className="mx-auto mt-2 max-w-md text-sm text-muted">{props.children}</div>}
    </div>
  )
}

/** Loading and error states of a query, so pages only render the success case. */
export function QueryState(props: { loading: boolean; error: Error | null; what: string }) {
  const t = useT()
  if (props.error) return <ErrorNote>{props.error.message}</ErrorNote>
  if (props.loading) return <p className="text-sm text-muted">{t.common.loading(props.what)}</p>
  return null
}

/** A destructive button that asks once more before it acts. */
export function ConfirmButton(props: { label: string; confirm: string; onConfirm: () => void; disabled?: boolean; pending?: boolean }) {
  const [armed, setArmed] = useState(false)
  const t = useT()
  if (!armed) {
    return (
      <Button variant="danger" type="button" disabled={props.disabled} onClick={() => setArmed(true)}>
        {props.label}
      </Button>
    )
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      <Button variant="danger" type="button" disabled={props.pending} onClick={props.onConfirm}>
        {props.pending ? t.common.working : props.confirm}
      </Button>
      <Button variant="ghost" type="button" disabled={props.pending} onClick={() => setArmed(false)}>
        {t.common.cancel}
      </Button>
    </span>
  )
}

/** A checkbox with a label and an optional explanation under it. */
export function CheckOption(props: {
  checked: boolean
  onChange: (v: boolean) => void
  label: ReactNode
  hint?: ReactNode
  disabled?: boolean
}) {
  return (
    <div className="flex items-center gap-1.5 text-sm">
      <label className={cx('flex items-center gap-3', props.disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer')}>
        <input type="checkbox" className="accent-[var(--color-accent)]" checked={props.checked} disabled={props.disabled} onChange={(e) => props.onChange(e.target.checked)} />
        <span>{props.label}</span>
      </label>
      {props.hint && <HelpTip label={typeof props.label === 'string' ? props.label : '?'}>{props.hint}</HelpTip>}
    </div>
  )
}

/** HTTP-only serving, for a CDN that terminates TLS itself and talks HTTP to this server. */
export function HttpOnlyChoice({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  const t = useT()
  return <CheckOption checked={checked} onChange={onChange} label={t.common.httpOnly} hint={t.common.httpOnlyHint} />
}

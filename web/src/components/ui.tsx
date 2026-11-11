import { useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes } from 'react'

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

type FieldProps = InputHTMLAttributes<HTMLInputElement> & { label: string; hint?: string }

export function Field({ label, hint, id, className, ...props }: FieldProps) {
  const inputId = id ?? props.name
  return (
    <div className="space-y-1.5">
      <label htmlFor={inputId} className="block text-xs text-muted">
        {label}
      </label>
      <input
        id={inputId}
        {...props}
        className={cx(
          'block h-9 w-full border border-line bg-bg px-3 text-sm text-fg placeholder:text-muted/60',
          'focus:border-accent focus:outline-none',
          className,
        )}
      />
      {hint && <p className="text-xs text-muted">{hint}</p>}
    </div>
  )
}

type SelectFieldProps = SelectHTMLAttributes<HTMLSelectElement> & { label: string; hint?: ReactNode }

/** A select that looks like Field. */
export function SelectField({ label, hint, id, className, children, ...props }: SelectFieldProps) {
  const selectId = id ?? props.name
  return (
    <div className="space-y-1.5">
      <label htmlFor={selectId} className="block text-xs text-muted">
        {label}
      </label>
      <select
        id={selectId}
        {...props}
        className={cx(
          'block h-9 w-full border border-line bg-bg px-2 text-sm text-fg',
          'focus:border-accent focus:outline-none disabled:opacity-60',
          className,
        )}
      >
        {children}
      </select>
      {hint && <p className="text-xs text-muted">{hint}</p>}
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
        <p className="mb-6 font-pixel text-xl text-accent">{props.product}</p>
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
  if (props.error) return <ErrorNote>{props.error.message}</ErrorNote>
  if (props.loading) return <p className="text-sm text-muted">Loading {props.what}…</p>
  return null
}

/** A destructive button that asks once more before it acts. */
export function ConfirmButton(props: { label: string; confirm: string; onConfirm: () => void; disabled?: boolean; pending?: boolean }) {
  const [armed, setArmed] = useState(false)
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
        {props.pending ? 'Working…' : props.confirm}
      </Button>
      <Button variant="ghost" type="button" disabled={props.pending} onClick={() => setArmed(false)}>
        Cancel
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
    <label className={cx('flex items-start gap-3 text-sm', props.disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer')}>
      <input
        type="checkbox"
        className="mt-0.5 accent-[var(--color-accent)]"
        checked={props.checked}
        disabled={props.disabled}
        onChange={(e) => props.onChange(e.target.checked)}
      />
      <span>
        {props.label}
        {props.hint && <span className="block text-xs text-muted">{props.hint}</span>}
      </span>
    </label>
  )
}

/** HTTP-only serving, for a CDN that terminates TLS itself and talks HTTP to this server. */
export function HttpOnlyChoice({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <CheckOption
      checked={checked}
      onChange={onChange}
      label="HTTP only"
      hint="For a CDN that handles HTTPS and talks plain HTTP to this server, like Cloudflare with SSL set to Flexible. No certificate here and no redirect, which would otherwise loop. Leave off unless you need it."
    />
  )
}

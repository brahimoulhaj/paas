import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from '@yusufceng1z/ding-react'
import { api, updatesQuery, type Updates } from '../lib/api'
import { useT } from '../lib/i18n'
import { rememberUpdated } from '../lib/toasts'
import { cx } from './ui'

const toastId = 'panel-update'

/**
 * UpdateCheck is the small button beside the version in the navbar: it checks for a new
 * panel version where the user is, without going to Settings. A new version shows a toast
 * with Update now and Dismiss; Update now runs the update in the same toast, waits for the
 * panel to come back on the new version and reloads into it.
 */
export function UpdateCheck({ updates }: { updates?: Updates }) {
  const t = useT()
  const m = t.settingsUpdates
  const queryClient = useQueryClient()
  const set = (next: Updates) => queryClient.setQueryData(updatesQuery.queryKey, next)

  // Pulls the image, hands over to the update helper, then polls until it records a result.
  const update = async (target: string, before: string) => {
    try {
      set(await api<Updates>('/updates/apply', { method: 'POST' }))
    } catch (e) {
      toast.error(m.updateFailedToast(target), { id: toastId, description: e instanceof Error ? e.message : String(e) })
      return
    }
    const started = Date.now()
    const poll = async () => {
      try {
        const u = await api<Updates>('/updates')
        const r = u.last_result
        if (r && r.finished_at !== before) {
          if (r.ok) {
            rememberUpdated(r.to)
            location.reload()
          } else {
            set(u)
            toast.error(m.updateFailedToast(r.to), { id: toastId, description: r.error ?? undefined })
          }
          return
        }
      } catch {
        // The panel is restarting; keep waiting.
      }
      if (Date.now() - started > 120_000) toast.loading(m.updatingToast(target), { id: toastId, description: m.slowNote })
      window.setTimeout(poll, 2000)
    }
    window.setTimeout(poll, 2000)
  }

  const check = useMutation({
    mutationFn: () => api<Updates>('/updates/check', { method: 'POST' }),
    onMutate: () => toast.loading(m.checkingToast, { id: toastId }),
    onSuccess: (next) => {
      set(next)
      if (!next.supported) {
        toast.info(m.unavailableLabel, { id: toastId, description: m.unavailable(next.reason ?? '') })
      } else if (next.error) {
        toast.error(m.checkFailedToast, { id: toastId, description: next.error })
      } else if (next.available && next.latest) {
        const target = next.latest
        toast.info(m.availableToast(target), {
          id: toastId,
          description: m.pullsNote(next.latest_image ?? target),
          duration: 0,
          action: {
            label: m.updateNow,
            onClick: () => {
              void update(target, next.last_result?.finished_at ?? '')
              return { type: 'loading', title: m.updatingToast(target), description: m.restartNote, action: null, secondary: null, duration: 0 }
            },
          },
          secondary: { label: t.overview.dismiss, onClick: () => {} },
        })
      } else {
        toast.success(m.upToDateToast(next.version), { id: toastId })
      }
    },
    onError: (e) => toast.error(m.checkFailedToast, { id: toastId, description: e.message }),
  })

  const busy = check.isPending || (!!updates && updates.status !== 'idle')
  return (
    <button
      type="button"
      onClick={() => check.mutate()}
      disabled={busy}
      aria-label={t.shell.checkUpdates}
      title={t.shell.checkUpdates}
      className="flex size-6 items-center justify-center text-muted hover:text-fg disabled:cursor-wait"
    >
      <svg viewBox="0 0 16 16" className={cx('size-3.5', busy && 'animate-spin')} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
        <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" />
        <path d="M13.5 2.5v3h-3" />
      </svg>
    </button>
  )
}

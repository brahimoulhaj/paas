import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Popover } from '@base-ui/react/popover'
import { useNavigate } from '@tanstack/react-router'
import { Toaster, toast } from '@yusufceng1z/ding-react'
import { activeDeploymentsQuery, api, type ActiveDeployment, type Deployment } from '../lib/api'
import { deploymentVersion, duration } from '../lib/format'
import { useT } from '../lib/i18n'
import { useTheme } from '../lib/theme'
import { takeUpdated } from '../lib/toasts'
import { Button } from './ui'

// ding's built-in tones: success rises, error falls, info is one note; loading stays silent.
// Browsers hold audio until the first click or key press on the page.
toast.configure({ sound: true, volume: 0.5 })

type ToastAction = NonNullable<NonNullable<Parameters<typeof toast.loading>[1]>['action']>

/**
 * Toasts for deployments on any app, wherever the user is in the panel: one when a deployment
 * starts or is queued, which turns into success or failure when it ends. It follows the same
 * list the overview alert polls, so finished deployments are noticed within one poll.
 */
export function DeployToasts() {
  const t = useT()
  const theme = useTheme()
  const navigate = useNavigate()
  const active = useQuery({ ...activeDeploymentsQuery, refetchInterval: 5_000 })
  // Deployments seen in progress, with the status last toasted for them.
  const seen = useRef(new Map<string, ActiveDeployment>())
  // The deployment whose Cancel was clicked, and the toast button the confirmation points at.
  const [confirm, setConfirm] = useState<{ d: ActiveDeployment; anchor: Element | null } | null>(null)

  // A panel update reloads the page into the new version; say it worked once it is up.
  useEffect(() => {
    const v = takeUpdated()
    if (v) toast.success(t.settingsUpdates.updatedToast(v))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once, on load
  }, [])

  useEffect(() => {
    if (!active.data) return
    // The toast's buttons. View opens the app's deployments; Cancel asks in a popover before
    // cancelling. While the deployment is in progress a click returns a patch that names both
    // buttons again, so the toast stays as it is; once it has finished, View closes it.
    const buttons = (d: ActiveDeployment): { action: ToastAction; secondary?: ToastAction } => {
      const keep = () => (seen.current.has(d.id) ? both : undefined)
      const action: ToastAction = {
        label: t.overview.viewDeployment,
        onClick: () => {
          navigate({ to: '/projects/$projectId/apps/$appId', params: { projectId: d.project_id, appId: d.app_id }, search: { tab: 'deployments' } })
          return keep()
        },
      }
      const secondary: ToastAction = {
        label: t.app.cancelDeployment,
        onClick: () => {
          setConfirm({ d, anchor: document.querySelector('.tst-viewport .tst-secondary') })
          return keep()
        },
      }
      const both = { action, secondary }
      return seen.current.has(d.id) ? both : { action }
    }
    const now = new Set(active.data.map((d) => d.id))

    for (const d of active.data) {
      const before = seen.current.get(d.id)
      seen.current.set(d.id, d)
      if (before?.status === d.status) continue
      const version = deploymentVersion(d).replace(/^—$/, '')
      toast.loading(d.status === 'running' ? t.overview.deployingApp(d.app_name) : t.overview.queuedApp(d.app_name), {
        id: d.id,
        description: version || undefined,
        ...buttons(d),
      })
    }

    for (const [id, d] of seen.current) {
      if (now.has(id)) continue
      seen.current.delete(id)
      setConfirm((c) => (c?.d.id === id ? null : c))
      api<Deployment>(`/deployments/${id}`)
        .then((done) => {
          const took = duration(done.started_at, done.finished_at)
          if (done.status === 'succeeded') toast.success(t.overview.deployedApp(d.app_name), { id, meta: took, ...buttons(d), duration: 8_000 })
          else if (done.status === 'failed') toast.error(t.overview.failedApp(d.app_name), { id, description: done.error || undefined, ...buttons(d) })
          else toast.info(t.overview.cancelledApp(d.app_name), { id })
        })
        .catch(() => toast.dismiss(id))
    }
  }, [active.data, t, navigate])

  return (
    <>
      <Toaster position="bottom-center" theme={theme.mode === 'dark' ? 'light' : 'dark'} labels={{ region: t.overview.toasts, dismiss: t.overview.dismiss }} />
      <CancelConfirm target={confirm} onClose={() => setConfirm(null)} />
    </>
  )
}

/** The confirmation above a deploy toast's Cancel button. */
function CancelConfirm({ target, onClose }: { target: { d: ActiveDeployment; anchor: Element | null } | null; onClose: () => void }) {
  const t = useT()
  const queryClient = useQueryClient()
  const cancel = useMutation({
    mutationFn: (id: string) => api<Deployment>(`/deployments/${id}/cancel`, { method: 'POST' }),
    onSuccess: () => {
      onClose()
      // Notice the end sooner than the next poll, so the toast turns to cancelled.
      setTimeout(() => queryClient.invalidateQueries({ queryKey: activeDeploymentsQuery.queryKey }), 1_000)
    },
  })
  return (
    <Popover.Root
      open={!!target}
      onOpenChange={(open) => {
        if (!open) {
          cancel.reset()
          onClose()
        }
      }}
    >
      <Popover.Portal>
        {/* Above ding's viewport, which sits at the highest z-index. */}
        <Popover.Positioner anchor={target?.anchor} side="top" sideOffset={6} className="z-[2147483647]">
          <Popover.Popup className="flex max-w-[calc(100vw-2rem)] flex-wrap items-center gap-1.5 border border-line bg-surface px-2 py-1.5 text-xs text-fg shadow-md">
            <Popover.Title className="me-1">{t.overview.cancelConfirmTitle}</Popover.Title>
            <Button variant="danger" type="button" className="h-6! px-2! text-[11px]! leading-none" disabled={cancel.isPending} onClick={() => target && cancel.mutate(target.d.id)}>
              {cancel.isPending ? t.common.working : t.overview.cancelYes}
            </Button>
            <Popover.Close render={<Button variant="ghost" type="button" className="h-6! px-2! text-[11px]! leading-none" />}>{t.overview.cancelNo}</Popover.Close>
            {cancel.error && <p className="w-full text-danger">{cancel.error.message}</p>}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  )
}

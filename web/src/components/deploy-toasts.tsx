import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Toaster, toast } from '@yusufceng1z/ding-react'
import { activeDeploymentsQuery, api, type ActiveDeployment, type Deployment } from '../lib/api'
import { deploymentVersion, duration } from '../lib/format'
import { useT } from '../lib/i18n'
import { useTheme } from '../lib/theme'
import { takeUpdated } from '../lib/toasts'

// ding's built-in tones: success rises, error falls, info is one note; loading stays silent.
// Browsers hold audio until the first click or key press on the page.
toast.configure({ sound: true, volume: 0.5 })

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

  // A panel update reloads the page into the new version; say it worked once it is up.
  useEffect(() => {
    const v = takeUpdated()
    if (v) toast.success(t.settingsUpdates.updatedToast(v))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once, on load
  }, [])

  useEffect(() => {
    if (!active.data) return
    const view = (d: ActiveDeployment) => ({
      label: t.overview.viewDeployment,
      onClick: () => {
        navigate({ to: '/projects/$projectId/apps/$appId', params: { projectId: d.project_id, appId: d.app_id }, search: { tab: 'deployments' } })
      },
    })
    const now = new Set(active.data.map((d) => d.id))

    for (const d of active.data) {
      const before = seen.current.get(d.id)
      seen.current.set(d.id, d)
      if (before?.status === d.status) continue
      const version = deploymentVersion(d).replace(/^—$/, '')
      toast.loading(d.status === 'running' ? t.overview.deployingApp(d.app_name) : t.overview.queuedApp(d.app_name), {
        id: d.id,
        description: version || undefined,
        action: view(d),
      })
    }

    for (const [id, d] of seen.current) {
      if (now.has(id)) continue
      seen.current.delete(id)
      api<Deployment>(`/deployments/${id}`)
        .then((done) => {
          const took = duration(done.started_at, done.finished_at)
          if (done.status === 'succeeded') toast.success(t.overview.deployedApp(d.app_name), { id, meta: took, action: view(d), duration: 8_000 })
          else if (done.status === 'failed') toast.error(t.overview.failedApp(d.app_name), { id, description: done.error || undefined, action: view(d) })
          else toast.info(t.overview.cancelledApp(d.app_name), { id })
        })
        .catch(() => toast.dismiss(id))
    }
  }, [active.data, t, navigate])

  return <Toaster position="bottom-center" theme={theme.mode === 'dark' ? 'light' : 'dark'} labels={{ region: t.overview.toasts, dismiss: t.overview.dismiss }} />
}

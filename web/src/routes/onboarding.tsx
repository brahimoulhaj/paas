import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { Button, cx, ErrorNote } from '../components/ui'
import { api, metaQuery, onboardingQuery } from '../lib/api'
import { rich, useT } from '../lib/i18n'
import { PanelDomain } from './settings'

const route = getRouteApi('/onboarding')

/**
 * Optional first-run steps after the owner is created. Every step can be skipped, and the
 * whole guide stays reachable from Settings. Later milestones add their steps here
 * (S3 connection, registry, notifications).
 */
const allSteps = (t: ReturnType<typeof useT>): { id: string; title: string; intro: string; body: () => ReactNode }[] => [
  {
    id: 'domain',
    title: t.onboarding.domainTitle,
    intro: t.onboarding.domainIntro,
    body: () => <PanelDomain bare />,
  },
  {
    id: 'deploy',
    title: t.onboarding.deployTitle,
    intro: t.onboarding.deployIntro,
    body: () => (
      <ul className="space-y-2 border border-line bg-surface p-4 text-sm text-muted">
        {[t.onboarding.projectLine, t.onboarding.appLine, t.onboarding.domainLine].map((line) => (
          <li key={line}>{rich(line, { b: (c) => <span className="text-fg">{c}</span> })}</li>
        ))}
      </ul>
    ),
  },
]

export function OnboardingPage() {
  const { step: stepParam } = route.useSearch()
  const { data: meta } = useSuspenseQuery(metaQuery)
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const t = useT()
  const steps = allSteps(t)
  const index = Math.min(Math.max(stepParam, 0), steps.length - 1)
  const step = steps[index]
  const last = index === steps.length - 1

  const finish = useMutation({
    mutationFn: () => api<void>('/onboarding/complete', { method: 'POST' }),
    onSuccess: (_, to: '/' | '/projects') => {
      queryClient.setQueryData(onboardingQuery.queryKey, { completed: true })
      navigate({ to })
    },
  })
  const go = (i: number) => navigate({ to: '/onboarding', search: { step: i } })

  return (
    <main className="flex min-h-full justify-center p-4 md:p-8">
      <div className="w-full max-w-2xl space-y-6">
        <div className="flex items-baseline justify-between gap-4">
          <p className="font-pixel text-xl text-accent">{meta.name}</p>
          <button type="button" className="text-xs text-muted hover:text-fg" disabled={finish.isPending} onClick={() => finish.mutate('/')}>
            {t.onboarding.skip}
          </button>
        </div>

        <ol className="flex flex-wrap gap-2" aria-label={t.onboarding.stepsLabel}>
          {steps.map((s, i) => (
            <li key={s.id}>
              <button
                type="button"
                onClick={() => go(i)}
                aria-current={i === index ? 'step' : undefined}
                className={cx(
                  'flex items-center gap-2 border px-3 py-1.5 text-xs',
                  i === index ? 'border-accent text-accent' : i < index ? 'border-line text-fg' : 'border-line text-muted',
                )}
              >
                <span className="font-pixel">{i + 1}</span>
                {s.title}
              </button>
            </li>
          ))}
        </ol>

        <div>
          <h1 className="font-pixel text-base uppercase">{step.title}</h1>
          <p className="mt-1 text-sm text-muted">{step.intro}</p>
        </div>

        {step.body()}

        {finish.error && <ErrorNote>{finish.error.message}</ErrorNote>}

        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line pt-4">
          <Button variant="ghost" type="button" disabled={index === 0} onClick={() => go(index - 1)}>
            {t.onboarding.back}
          </Button>
          {last ? (
            <span className="flex flex-wrap gap-2">
              <Button variant="ghost" type="button" disabled={finish.isPending} onClick={() => finish.mutate('/')}>
                {t.onboarding.goToOverview}
              </Button>
              <Button type="button" disabled={finish.isPending} onClick={() => finish.mutate('/projects')}>
                {t.onboarding.createProject}
              </Button>
            </span>
          ) : (
            <Button type="button" onClick={() => go(index + 1)}>
              {t.onboarding.next}
            </Button>
          )}
        </div>
      </div>
    </main>
  )
}

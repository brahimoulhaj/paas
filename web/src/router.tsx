import type { QueryClient } from '@tanstack/react-query'
import { createRootRouteWithContext, createRoute, createRouter, Outlet, redirect } from '@tanstack/react-router'
import { Shell } from './components/shell'
import { meQuery, metaQuery, onboardingQuery } from './lib/api'
import { ActivityPage } from './routes/activity'
import { AppPage, appTabs, type AppTab } from './routes/app'
import { LoginPage } from './routes/login'
import { OnboardingPage } from './routes/onboarding'
import { OverviewPage } from './routes/overview'
import { ProjectPage } from './routes/project'
import { ProjectsPage } from './routes/projects'
import { SettingsPage } from './routes/settings'
import { SetupPage } from './routes/setup'

type Ctx = { queryClient: QueryClient }

// Routing rules: no owner yet -> /setup; signed out -> /login; otherwise the app.
const rootRoute = createRootRouteWithContext<Ctx>()({
  component: Outlet,
  errorComponent: ({ error }) => (
    <main className="flex min-h-full items-center justify-center p-4">
      <div className="max-w-sm border border-danger/60 p-6 text-sm">
        <p className="font-pixel text-danger uppercase">Something went wrong</p>
        <p className="mt-2 text-muted">{error instanceof Error ? error.message : String(error)}</p>
        <button className="mt-4 border border-line px-3 py-1 hover:border-line-strong" onClick={() => location.reload()}>
          Reload
        </button>
      </div>
    </main>
  ),
})

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  validateSearch: (s: Record<string, unknown>) => ({ token: typeof s.token === 'string' ? s.token : '' }),
  beforeLoad: async ({ context }) => {
    const meta = await context.queryClient.ensureQueryData(metaQuery)
    if (!meta.setup_required) throw redirect({ to: '/login' })
  },
  component: SetupPage,
})

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  beforeLoad: async ({ context }) => {
    const meta = await context.queryClient.ensureQueryData(metaQuery)
    if (meta.setup_required) throw redirect({ to: '/setup', search: { token: '' } })
    if (await context.queryClient.ensureQueryData(meQuery)) throw redirect({ to: '/' })
  },
  component: LoginPage,
})

async function requireOwner(queryClient: QueryClient) {
  const meta = await queryClient.ensureQueryData(metaQuery)
  if (meta.setup_required) throw redirect({ to: '/setup', search: { token: '' } })
  if (!(await queryClient.ensureQueryData(meQuery))) throw redirect({ to: '/login' })
}

// Optional first-run steps, outside the shell. Reachable again from Settings.
const onboardingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/onboarding',
  validateSearch: (s: Record<string, unknown>): { step: number } => {
    const n = Number(s.step)
    return { step: Number.isInteger(n) && n >= 0 ? n : 0 }
  },
  beforeLoad: ({ context }) => requireOwner(context.queryClient),
  component: OnboardingPage,
})

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  beforeLoad: async ({ context }) => {
    await requireOwner(context.queryClient)
    const onboarding = await context.queryClient.ensureQueryData(onboardingQuery)
    if (!onboarding.completed) throw redirect({ to: '/onboarding', search: { step: 0 } })
  },
  component: Shell,
})

const overviewRoute = createRoute({ getParentRoute: () => appRoute, path: '/', component: OverviewPage })

const projectsRoute = createRoute({ getParentRoute: () => appRoute, path: '/projects', component: ProjectsPage })
const projectRoute = createRoute({ getParentRoute: () => appRoute, path: '/projects/$projectId', component: ProjectPage })
const appPageRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/projects/$projectId/apps/$appId',
  validateSearch: (s: Record<string, unknown>): { tab?: AppTab } =>
    appTabs.includes(s.tab as AppTab) ? { tab: s.tab as AppTab } : {},
  component: AppPage,
})
const activityRoute = createRoute({ getParentRoute: () => appRoute, path: '/activity', component: ActivityPage })
const settingsRoute = createRoute({ getParentRoute: () => appRoute, path: '/settings', component: SettingsPage })

const routeTree = rootRoute.addChildren([
  setupRoute,
  loginRoute,
  onboardingRoute,
  appRoute.addChildren([overviewRoute, projectsRoute, projectRoute, appPageRoute, activityRoute, settingsRoute]),
])

export function makeRouter(queryClient: QueryClient) {
  return createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: false,
    defaultNotFoundComponent: () => (
      <main className="flex min-h-full items-center justify-center p-4 text-sm text-muted">
        <p>
          <span className="font-pixel text-fg">404</span> — nothing here. <a className="text-accent underline" href="/">Go home</a>
        </p>
      </main>
    ),
  })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof makeRouter>
  }
}

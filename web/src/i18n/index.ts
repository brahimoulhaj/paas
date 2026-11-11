// All texts of the web app, by language and then by screen. Each file exports en, fr, ar
// and es; en is the source, and the others are typed against it so a missing text fails
// the typecheck.
import * as common from './common'
import * as shell from './shell'
import * as auth from './auth'
import * as theme from './theme'
import * as app from './app'
import * as logviewer from './logviewer'
import * as dns from './dns'
import * as project from './project'
import * as projects from './projects'
import * as overview from './overview'
import * as activity from './activity'
import * as source from './source'
import * as format from './format'
import * as settings from './settings'
import * as settingsGit from './settingsGit'
import * as settingsTokens from './settingsTokens'
import * as settingsUpdates from './settingsUpdates'
import * as onboarding from './onboarding'

type Code = 'en' | 'fr' | 'ar' | 'es'

const pick = (l: Code) => ({
  common: common[l] as typeof common.en,
  shell: shell[l] as typeof shell.en,
  auth: auth[l] as typeof auth.en,
  theme: theme[l] as typeof theme.en,
  app: app[l] as typeof app.en,
  logviewer: logviewer[l] as typeof logviewer.en,
  dns: dns[l] as typeof dns.en,
  project: project[l] as typeof project.en,
  projects: projects[l] as typeof projects.en,
  overview: overview[l] as typeof overview.en,
  activity: activity[l] as typeof activity.en,
  source: source[l] as typeof source.en,
  format: format[l] as typeof format.en,
  settings: settings[l] as typeof settings.en,
  settingsGit: settingsGit[l] as typeof settingsGit.en,
  settingsTokens: settingsTokens[l] as typeof settingsTokens.en,
  settingsUpdates: settingsUpdates[l] as typeof settingsUpdates.en,
  onboarding: onboarding[l] as typeof onboarding.en,
})

export type Messages = ReturnType<typeof pick>

export const messages: Record<Code, Messages> = { en: pick('en'), fr: pick('fr'), ar: pick('ar'), es: pick('es') }

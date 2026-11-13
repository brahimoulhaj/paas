import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { gitBranchesQuery, gitReposQuery, gitSourcesQuery, registriesQuery, type App, type GitSource, type SourceInput } from '../lib/api'
import { repoName } from '../lib/format'
import { rich, useT } from '../lib/i18n'
import { ComboField, type ComboOption } from './combobox'
import { cx, Field } from './ui'

/** The source part of an app form, kept as strings while editing. */
export type SourceForm = {
  source_type: 'image' | 'git'
  image: string
  /** Private registry for the image; empty = a public image. */
  registry_id: string
  /** Git connection; empty = a public repository by URL. */
  git_source_id: string
  git_repository: string
  git_branch: string
  builder: 'auto' | 'dockerfile' | 'railpack'
  build_context: string
  dockerfile_path: string
}

export function sourceForm(app?: App): SourceForm {
  return {
    source_type: app?.source_type ?? 'image',
    image: app?.image ?? '',
    registry_id: app?.registry_id ?? '',
    git_source_id: app?.git_source_id ?? '',
    git_repository: app?.git_repository ?? '',
    git_branch: app?.git_branch || 'main',
    builder: (app?.builder as SourceForm['builder']) || 'auto',
    build_context: app?.build_context ?? '',
    dockerfile_path: app?.dockerfile_path || 'Dockerfile',
  }
}

/** The request fields for the chosen source; the other source's fields are left out. */
export function sourceInput(f: SourceForm): SourceInput {
  if (f.source_type === 'image') return { source_type: 'image', image: f.image.trim(), registry_id: f.registry_id }
  return {
    source_type: 'git',
    git_source_id: f.git_source_id,
    git_repository: f.git_repository.trim(),
    git_branch: f.git_branch.trim(),
    builder: f.builder,
    build_context: f.build_context.trim(),
    dockerfile_path: f.dockerfile_path.trim(),
  }
}

/** Whether the form differs from the app's saved source. */
export function sourceChanged(f: SourceForm, app: App): boolean {
  return JSON.stringify(sourceInput(sourceForm(app))) !== JSON.stringify(sourceInput(f))
}

const sourceKind = { github_app: 'GitHub', gitlab: 'GitLab', deploy_key: 'SSH' } as const
const sourceLabel = (s: GitSource) => `${sourceKind[s.type]} · ${s.name}`

/** Image or Git repository, and the fields of the chosen one. */
export function SourceFields({ form, onChange }: { form: SourceForm; onChange: (f: SourceForm) => void }) {
  const t = useT()
  const choices: { value: SourceForm['source_type']; label: string }[] = [
    { value: 'image', label: t.source.image },
    { value: 'git', label: t.source.gitRepository },
  ]
  return (
    <div className="space-y-4">
      <div role="radiogroup" aria-label={t.source.source} className="inline-flex border border-line">
        {choices.map((c) => (
          <button
            key={c.value}
            type="button"
            role="radio"
            aria-checked={form.source_type === c.value}
            onClick={() => onChange({ ...form, source_type: c.value })}
            className={cx(
              'h-8 px-3 text-sm transition-colors',
              form.source_type === c.value ? 'bg-accent text-accent-fg' : 'text-muted hover:text-fg',
            )}
          >
            {c.label}
          </button>
        ))}
      </div>
      {form.source_type === 'image' ? (
        <ImageFields form={form} onChange={onChange} />
      ) : (
        <GitFields form={form} onChange={onChange} />
      )}
    </div>
  )
}

/** The image, and the registry it is pulled from when it is private. */
function ImageFields({ form, onChange }: { form: SourceForm; onChange: (f: SourceForm) => void }) {
  const t = useT()
  const registries = useQuery(registriesQuery)
  const chosen = registries.data?.find((r) => r.id === form.registry_id)
  const options: ComboOption[] = [
    { value: '', label: t.source.publicImage },
    ...(registries.data ?? []).map((r) => ({ value: r.id, label: `${r.name} · ${r.username}@${r.server}` })),
  ]
  if (form.registry_id && !options.some((o) => o.value === form.registry_id)) {
    options.push({ value: form.registry_id, label: t.source.registryAccess })
  }
  // Docker Hub images carry no host; others start with it.
  const placeholder = chosen && chosen.server !== 'docker.io' ? `${chosen.server}/org/app:1.0` : 'nginx:1.27'
  return (
    <>
      <ComboField
        label={t.source.registryAccess}
        name="registry_id"
        options={options}
        value={form.registry_id}
        onChange={(id) => onChange({ ...form, registry_id: id })}
        hint={rich(t.source.registryHint, {
          a: (c) => (
            <Link to="/registry" className="text-accent hover:underline">
              {c}
            </Link>
          ),
        })}
      />
      <Field
        label={t.source.image}
        name="image"
        required
        dir="ltr"
        placeholder={placeholder}
        hintTooltip
        hint={chosen && chosen.server !== 'docker.io' ? t.source.imageOnRegistry(chosen.server) : t.source.imageHint}
        value={form.image}
        onChange={(e) => onChange({ ...form, image: e.target.value })}
      />
    </>
  )
}

function GitFields({ form, onChange }: { form: SourceForm; onChange: (f: SourceForm) => void }) {
  const set = (key: keyof SourceForm) => (e: { target: { value: string } }) => onChange({ ...form, [key]: e.target.value })
  const t = useT()
  const sources = useQuery(gitSourcesQuery)
  const connected = form.git_source_id !== ''
  // A deploy key lists nothing: the repository is typed as an SSH URL.
  const deployKey = sources.data?.find((x) => x.id === form.git_source_id)?.type === 'deploy_key'
  const accessOptions: ComboOption[] = [
    { value: '', label: t.source.publicByUrl },
    ...(sources.data ?? []).map((x) => ({ value: x.id, label: sourceLabel(x) })),
  ]
  // The app's connection, while the list is loading or if it could not be loaded.
  if (connected && !accessOptions.some((o) => o.value === form.git_source_id)) {
    accessOptions.push({ value: form.git_source_id, label: t.source.gitConnection })
  }

  return (
    <>
      <ComboField
        label={t.source.repositoryAccess}
        name="git_source_id"
        options={accessOptions}
        value={form.git_source_id}
        onChange={(id) => id !== form.git_source_id && onChange({ ...form, git_source_id: id, git_repository: '', git_branch: 'main' })}
        searchPlaceholder={t.source.searchConnections}
        hint={rich(t.source.accessHint, {
          a: (c) => (
            <Link to="/settings" search={{ tab: 'git' }} className="text-accent hover:underline">
              {c}
            </Link>
          ),
        })}
      />

      {deployKey ? (
        <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
          <Field
            label={t.source.sshUrl}
            name="git_repository"
            required
            dir="ltr"
            pattern="(ssh://)?[A-Za-z0-9._\-]+@.+"
            placeholder="git@github.com:org/app.git"
            hintTooltip
            hint={t.source.sshUrlHint}
            value={form.git_repository}
            onChange={set('git_repository')}
            autoComplete="off"
            spellCheck={false}
          />
          <Field label={t.source.branch} name="git_branch" required dir="ltr" placeholder="main" value={form.git_branch} onChange={set('git_branch')} />
        </div>
      ) : connected ? (
        <ConnectedRepo form={form} onChange={onChange} />
      ) : (
        <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
          <Field
            label={t.source.repositoryUrl}
            name="git_repository"
            type="url"
            required
            dir="ltr"
            pattern="https://.+"
            title={t.source.urlTitle}
            placeholder="https://github.com/org/app.git"
            value={form.git_repository}
            onChange={set('git_repository')}
          />
          <Field label={t.source.branch} name="git_branch" required dir="ltr" placeholder="main" value={form.git_branch} onChange={set('git_branch')} />
        </div>
      )}

      <ComboField
        label={t.source.builder}
        name="builder"
        options={[
          { value: 'auto', label: t.source.builderAuto, note: t.source.builderAutoNote },
          { value: 'dockerfile', label: 'Dockerfile' },
          { value: 'railpack', label: 'Railpack', note: t.source.builderRailpackNote },
        ]}
        value={form.builder}
        onChange={(b) => onChange({ ...form, builder: b as SourceForm['builder'] })}
        hint={t.source.builderHint}
      />

      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label={t.source.buildContext}
          name="build_context"
          dir="ltr"
          placeholder="."
          hintTooltip hint={t.source.buildContextHint}
          value={form.build_context}
          onChange={set('build_context')}
        />
        {form.builder !== 'railpack' && (
          <Field
            label="Dockerfile"
            name="dockerfile_path"
            required
            dir="ltr"
            placeholder="Dockerfile"
            hintTooltip hint={t.source.dockerfileHint}
            value={form.dockerfile_path}
            onChange={set('dockerfile_path')}
          />
        )}
      </div>
    </>
  )
}

/** Repository and branch pickers for a Git connection. */
function ConnectedRepo({ form, onChange }: { form: SourceForm; onChange: (f: SourceForm) => void }) {
  const t = useT()
  const repos = useQuery(gitReposQuery(form.git_source_id))
  const selected = repos.data?.find((r) => r.clone_url === form.git_repository)
  const branches = useQuery({ ...gitBranchesQuery(form.git_source_id, selected?.full_name ?? ''), enabled: !!selected })
  // The saved values stay selectable when the lists do not (or no longer) hold them.
  const repoMissing = form.git_repository !== '' && !selected
  const branchMissing = form.git_branch !== '' && !!branches.data && !branches.data.includes(form.git_branch)

  const repoOptions: ComboOption[] = (repos.data ?? []).map((r) => ({
    value: r.clone_url,
    label: r.full_name,
    note: r.private ? undefined : t.source.public,
  }))
  if (repoMissing) repoOptions.unshift({ value: form.git_repository, label: repoName(form.git_repository) })
  const branchOptions: ComboOption[] = (branches.data ?? []).map((b) => ({ value: b, label: b }))
  if (branchMissing) branchOptions.unshift({ value: form.git_branch, label: form.git_branch })

  function pickRepo(cloneUrl: string) {
    if (cloneUrl === form.git_repository) return
    const r = repos.data?.find((x) => x.clone_url === cloneUrl)
    onChange({ ...form, git_repository: cloneUrl, git_branch: r?.default_branch || 'main' })
  }

  return (
    <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
      <ComboField
        label={t.source.repository}
        name="git_repository"
        required
        options={repoOptions}
        value={form.git_repository}
        onChange={pickRepo}
        placeholder={repos.isPending ? t.source.loadingRepositories : t.source.chooseRepository}
        searchPlaceholder={t.source.searchRepositories}
        empty={t.source.noRepositoryMatches}
        disabled={repos.isPending}
        hint={
          repos.error ? (
            <span className="text-danger">{repos.error.message}</span>
          ) : repos.data?.length === 0 ? (
            t.source.noRepositories
          ) : undefined
        }
      />
      {branches.data ? (
        <ComboField
          label={t.source.branch}
          name="git_branch"
          required
          options={branchOptions}
          value={form.git_branch}
          onChange={(b) => onChange({ ...form, git_branch: b })}
          placeholder={t.source.chooseBranch}
          searchPlaceholder={t.source.searchBranches}
          empty={t.source.noBranchMatches}
        />
      ) : (
        <Field
          label={t.source.branch}
          name="git_branch"
          required
          dir="ltr"
          placeholder="main"
          hint={branches.error ? branches.error.message : undefined}
          value={form.git_branch}
          onChange={(e) => onChange({ ...form, git_branch: e.target.value })}
        />
      )}
    </div>
  )
}

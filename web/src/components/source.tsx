import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { gitBranchesQuery, gitReposQuery, gitSourcesQuery, type App, type GitSource, type SourceInput } from '../lib/api'
import { repoName } from '../lib/format'
import { cx, Field, SelectField } from './ui'

/** The source part of an app form, kept as strings while editing. */
export type SourceForm = {
  source_type: 'image' | 'git'
  image: string
  /** Git connection; empty = a public repository by URL. */
  git_source_id: string
  git_repository: string
  git_branch: string
  build_context: string
  dockerfile_path: string
}

export function sourceForm(app?: App): SourceForm {
  return {
    source_type: app?.source_type ?? 'image',
    image: app?.image ?? '',
    git_source_id: app?.git_source_id ?? '',
    git_repository: app?.git_repository ?? '',
    git_branch: app?.git_branch || 'main',
    build_context: app?.build_context ?? '',
    dockerfile_path: app?.dockerfile_path || 'Dockerfile',
  }
}

/** The request fields for the chosen source; the other source's fields are left out. */
export function sourceInput(f: SourceForm): SourceInput {
  if (f.source_type === 'image') return { source_type: 'image', image: f.image.trim() }
  return {
    source_type: 'git',
    git_source_id: f.git_source_id,
    git_repository: f.git_repository.trim(),
    git_branch: f.git_branch.trim(),
    builder: 'dockerfile',
    build_context: f.build_context.trim(),
    dockerfile_path: f.dockerfile_path.trim(),
  }
}

/** Whether the form differs from the app's saved source. */
export function sourceChanged(f: SourceForm, app: App): boolean {
  return JSON.stringify(sourceInput(sourceForm(app))) !== JSON.stringify(sourceInput(f))
}

const choices: { value: SourceForm['source_type']; label: string }[] = [
  { value: 'image', label: 'Image' },
  { value: 'git', label: 'Git repository' },
]

const sourceLabel = (s: GitSource) => `${s.type === 'github_app' ? 'GitHub' : 'GitLab'} · ${s.name}`

/** Image or Git repository, and the fields of the chosen one. */
export function SourceFields({ form, onChange }: { form: SourceForm; onChange: (f: SourceForm) => void }) {
  const set = (key: keyof SourceForm) => (e: { target: { value: string } }) => onChange({ ...form, [key]: e.target.value })
  return (
    <div className="space-y-4">
      <div role="radiogroup" aria-label="Source" className="inline-flex border border-line">
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
        <Field
          label="Image"
          name="image"
          required
          placeholder="nginx:1.27"
          hint="Pulled from a public registry on every deploy."
          value={form.image}
          onChange={set('image')}
        />
      ) : (
        <GitFields form={form} onChange={onChange} />
      )}
    </div>
  )
}

function GitFields({ form, onChange }: { form: SourceForm; onChange: (f: SourceForm) => void }) {
  const set = (key: keyof SourceForm) => (e: { target: { value: string } }) => onChange({ ...form, [key]: e.target.value })
  const sources = useQuery(gitSourcesQuery)
  const connected = form.git_source_id !== ''

  return (
    <>
      <SelectField
        label="Repository access"
        name="git_source_id"
        value={form.git_source_id}
        onChange={(e) => onChange({ ...form, git_source_id: e.target.value, git_repository: '', git_branch: 'main' })}
        hint={
          <>
            Private repositories need a Git connection.{' '}
            <Link to="/settings" className="text-accent hover:underline">
              Connect GitHub or GitLab in Settings
            </Link>
            .
          </>
        }
      >
        <option value="">Public repository, by URL</option>
        {sources.data?.map((s) => (
          <option key={s.id} value={s.id}>
            {sourceLabel(s)}
          </option>
        ))}
        {/* The app's connection, while the list is loading or if it could not be loaded. */}
        {connected && !sources.data?.some((s) => s.id === form.git_source_id) && <option value={form.git_source_id}>Git connection</option>}
      </SelectField>

      {connected ? (
        <ConnectedRepo form={form} onChange={onChange} />
      ) : (
        <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
          <Field
            label="Repository URL"
            name="git_repository"
            type="url"
            required
            pattern="https://.+"
            title="An https URL"
            placeholder="https://github.com/org/app.git"
            value={form.git_repository}
            onChange={set('git_repository')}
          />
          <Field label="Branch" name="git_branch" required placeholder="main" value={form.git_branch} onChange={set('git_branch')} />
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label="Build context"
          name="build_context"
          placeholder="."
          hint="Folder inside the repository. Empty = its root."
          value={form.build_context}
          onChange={set('build_context')}
        />
        <Field
          label="Dockerfile"
          name="dockerfile_path"
          required
          placeholder="Dockerfile"
          hint="Path from the build context."
          value={form.dockerfile_path}
          onChange={set('dockerfile_path')}
        />
      </div>
      <p className="text-xs text-muted">Each deploy fetches the branch tip and builds it with its Dockerfile on this server.</p>
    </>
  )
}

/** Repository and branch pickers for a Git connection. */
function ConnectedRepo({ form, onChange }: { form: SourceForm; onChange: (f: SourceForm) => void }) {
  const repos = useQuery(gitReposQuery(form.git_source_id))
  const selected = repos.data?.find((r) => r.clone_url === form.git_repository)
  const branches = useQuery({ ...gitBranchesQuery(form.git_source_id, selected?.full_name ?? ''), enabled: !!selected })
  // The saved values stay selectable when the lists do not (or no longer) hold them.
  const repoMissing = form.git_repository !== '' && !selected
  const branchMissing = form.git_branch !== '' && !!branches.data && !branches.data.includes(form.git_branch)

  function pickRepo(cloneUrl: string) {
    const r = repos.data?.find((x) => x.clone_url === cloneUrl)
    onChange({ ...form, git_repository: cloneUrl, git_branch: r?.default_branch || 'main' })
  }

  return (
    <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
      <SelectField
        label="Repository"
        name="git_repository"
        required
        value={form.git_repository}
        onChange={(e) => pickRepo(e.target.value)}
        hint={
          repos.error ? (
            <span className="text-danger">{repos.error.message}</span>
          ) : repos.data?.length === 0 ? (
            'This connection can read no repository yet. Give it access to some in Settings.'
          ) : undefined
        }
      >
        <option value="" disabled>
          {repos.isPending ? 'Loading repositories…' : 'Choose a repository'}
        </option>
        {repoMissing && <option value={form.git_repository}>{repoName(form.git_repository)}</option>}
        {repos.data?.map((r) => (
          <option key={r.clone_url} value={r.clone_url}>
            {r.full_name}
            {r.private ? '' : ' (public)'}
          </option>
        ))}
      </SelectField>
      {branches.data ? (
        <SelectField label="Branch" name="git_branch" required value={form.git_branch} onChange={(e) => onChange({ ...form, git_branch: e.target.value })}>
          {branchMissing && <option value={form.git_branch}>{form.git_branch}</option>}
          {branches.data.map((b) => (
            <option key={b} value={b}>
              {b}
            </option>
          ))}
        </SelectField>
      ) : (
        <Field
          label="Branch"
          name="git_branch"
          required
          placeholder="main"
          hint={branches.error ? branches.error.message : undefined}
          value={form.git_branch}
          onChange={(e) => onChange({ ...form, git_branch: e.target.value })}
        />
      )}
    </div>
  )
}

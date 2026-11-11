import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useState, type FormEvent } from 'react'
import { AuthCard, Button, ErrorNote, Field } from '../components/ui'
import { api, meQuery, metaQuery, type Meta, type User } from '../lib/api'

const route = getRouteApi('/setup')

export function SetupPage() {
  const { token } = route.useSearch()
  const { data: meta } = useSuspenseQuery(metaQuery)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [mismatch, setMismatch] = useState(false)

  const setup = useMutation({
    mutationFn: () => api<User>('/setup', { method: 'POST', body: { token, email, password } }),
    onSuccess: (user) => {
      queryClient.setQueryData<Meta>(metaQuery.queryKey, (m) => m && { ...m, setup_required: false })
      queryClient.setQueryData(meQuery.queryKey, user)
      navigate({ to: '/onboarding', search: { step: 0 } })
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    const differs = password !== confirm
    setMismatch(differs)
    if (!differs) setup.mutate()
  }

  if (!token) {
    return (
      <AuthCard product={meta.name} title="Setup link needed" subtitle="This server has no owner yet.">
        <p className="text-sm text-muted">
          Open the setup link printed by the installer. If it has expired, run this on the server for a new one:
        </p>
        <pre className="mt-3 overflow-x-auto border border-line bg-bg px-3 py-2 text-xs text-accent">
          {meta.slug} setup-token
        </pre>
      </AuthCard>
    )
  }

  return (
    <AuthCard product={meta.name} title="Create the owner" subtitle="This account controls the whole server.">
      <form onSubmit={submit} className="space-y-4">
        <Field label="Email" name="email" type="email" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
        <Field label="Password" name="password" type="password" autoComplete="new-password" required minLength={8} hint="At least 8 characters." value={password} onChange={(e) => setPassword(e.target.value)} />
        <Field label="Confirm password" name="confirm" type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        {mismatch && <ErrorNote>The two passwords do not match.</ErrorNote>}
        {setup.error && !mismatch && <ErrorNote>{setup.error.message}</ErrorNote>}
        <Button type="submit" className="w-full" disabled={setup.isPending}>
          {setup.isPending ? 'Creating…' : 'Create owner'}
        </Button>
      </form>
    </AuthCard>
  )
}

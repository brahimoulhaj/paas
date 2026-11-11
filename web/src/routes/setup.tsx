import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useState, type FormEvent } from 'react'
import { AuthCard, Button, ErrorNote, Field } from '../components/ui'
import { api, meQuery, metaQuery, type Meta, type User } from '../lib/api'
import { useT } from '../lib/i18n'

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
  const t = useT()

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
      <AuthCard product={meta.name} title={t.auth.setupNeeded} subtitle={t.auth.noOwnerYet}>
        <p className="text-sm text-muted">{t.auth.setupNeededBody}</p>
        <pre dir="ltr" className="mt-3 overflow-x-auto border border-line bg-bg px-3 py-2 text-xs text-accent">
          {meta.slug} setup-token
        </pre>
      </AuthCard>
    )
  }

  return (
    <AuthCard product={meta.name} title={t.auth.createOwnerTitle} subtitle={t.auth.createOwnerSubtitle}>
      <form onSubmit={submit} className="space-y-4">
        <Field label={t.common.email} name="email" type="email" dir="ltr" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
        <Field label={t.common.password} name="password" type="password" dir="ltr" autoComplete="new-password" required minLength={8} hint={t.auth.passwordHint} value={password} onChange={(e) => setPassword(e.target.value)} />
        <Field label={t.auth.confirmPassword} name="confirm" type="password" dir="ltr" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        {mismatch && <ErrorNote>{t.auth.mismatch}</ErrorNote>}
        {setup.error && !mismatch && <ErrorNote>{setup.error.message}</ErrorNote>}
        <Button type="submit" className="w-full" disabled={setup.isPending}>
          {setup.isPending ? t.common.creating : t.auth.createOwner}
        </Button>
      </form>
    </AuthCard>
  )
}

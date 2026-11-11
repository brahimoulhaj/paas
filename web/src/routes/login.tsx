import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useState, type FormEvent } from 'react'
import { AuthCard, Button, ErrorNote, Field } from '../components/ui'
import { api, meQuery, metaQuery, type User } from '../lib/api'
import { useT } from '../lib/i18n'

export function LoginPage() {
  const { data: meta } = useSuspenseQuery(metaQuery)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const t = useT()

  const login = useMutation({
    mutationFn: () => api<User>('/auth/login', { method: 'POST', body: { email, password } }),
    onSuccess: (user) => {
      queryClient.setQueryData(meQuery.queryKey, user)
      navigate({ to: '/' })
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    login.mutate()
  }

  return (
    <AuthCard product={meta.name} title={t.auth.signIn} subtitle={t.auth.signInSubtitle}>
      <form onSubmit={submit} className="space-y-4">
        <Field label={t.common.email} name="email" type="email" dir="ltr" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
        <Field label={t.common.password} name="password" dir="ltr" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        {login.error && <ErrorNote>{login.error.message}</ErrorNote>}
        <Button type="submit" className="w-full" disabled={login.isPending}>
          {login.isPending ? t.auth.signingIn : t.auth.signIn}
        </Button>
      </form>
    </AuthCard>
  )
}

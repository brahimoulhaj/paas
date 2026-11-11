import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useState, type FormEvent } from 'react'
import { AuthCard, Button, ErrorNote, Field } from '../components/ui'
import { api, meQuery, metaQuery, type User } from '../lib/api'

export function LoginPage() {
  const { data: meta } = useSuspenseQuery(metaQuery)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

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
    <AuthCard product={meta.name} title="Sign in" subtitle="Use the owner account for this server.">
      <form onSubmit={submit} className="space-y-4">
        <Field label="Email" name="email" type="email" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
        <Field label="Password" name="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        {login.error && <ErrorNote>{login.error.message}</ErrorNote>}
        <Button type="submit" className="w-full" disabled={login.isPending}>
          {login.isPending ? 'Signing in…' : 'Sign in'}
        </Button>
      </form>
    </AuthCard>
  )
}

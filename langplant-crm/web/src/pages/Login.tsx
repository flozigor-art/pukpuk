import { Sprout } from 'lucide-react'
import { useState } from 'react'
import { Button, Field } from '../components/ui'
import { post } from '../lib/api'
import { queryClient } from '../lib/queries'

export function Login({ onLogin }: { onLogin: () => void }) {
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await post('/auth/login', { login, password })
      queryClient.clear()
      onLogin()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="login">
      <form className="card" onSubmit={submit}>
        <span className="brand-logo" style={{ width: 40, height: 40, borderRadius: 12 }}>
          <Sprout size={22} />
        </span>
        <h1>LangPlant CRM</h1>
        <p className="muted" style={{ marginBottom: 20 }}>
          Ролики, архив материалов, план публикаций и музыка
        </p>
        <div className="form">
          <Field label="Логин">
            <input className="input" value={login} onChange={(e) => setLogin(e.target.value)} autoComplete="username" autoCapitalize="none" autoFocus required />
          </Field>
          <Field label="Пароль">
            <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
          </Field>
          {error && <div className="banner red">{error}</div>}
          <Button variant="primary" size="lg" block loading={busy} type="submit">
            Войти
          </Button>
        </div>
      </form>
    </div>
  )
}

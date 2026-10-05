import '@fontsource-variable/inter'
import './styles.css'
import { QueryClientProvider, useQuery } from '@tanstack/react-query'
import { StrictMode, Suspense, lazy, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router'
import { Toaster } from 'sonner'
import { applyTheme, Layout, savedTheme } from './components/Layout'
import { ConfirmProvider, Loading } from './components/ui'
import { ApiError, get, setUnauthorizedHandler } from './lib/api'
import { queryClient, useBootstrapQuery, useServerEvents } from './lib/queries'
import type { User } from './lib/types'
import { Login } from './pages/Login'

const Dashboard = lazy(() => import('./pages/Dashboard'))
const Videos = lazy(() => import('./pages/Videos'))
const VideoPage = lazy(() => import('./pages/VideoPage'))
const Calendar = lazy(() => import('./pages/Calendar'))
const MusicPage = lazy(() => import('./pages/Music'))
const Storage = lazy(() => import('./pages/Storage'))
const Trash = lazy(() => import('./pages/Trash'))
const SettingsPage = lazy(() => import('./pages/Settings'))

applyTheme(savedTheme())

function Authed() {
  const boot = useBootstrapQuery()
  useServerEvents(boot.isSuccess)
  if (boot.isError) {
    return (
      <div className="center" style={{ minHeight: '100dvh' }}>
        <div className="empty">
          <h3>Не удалось загрузить данные</h3>
          <div className="small">{(boot.error as Error).message}</div>
          <button className="btn" onClick={() => boot.refetch()}>
            Повторить
          </button>
        </div>
      </div>
    )
  }
  if (!boot.data) return <Loading />
  return (
    <Suspense fallback={<Loading />}>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Dashboard />} />
          <Route path="videos" element={<Videos />} />
          <Route path="videos/:id" element={<VideoPage />} />
          <Route path="calendar" element={<Calendar />} />
          <Route path="music" element={<MusicPage />} />
          <Route path="storage" element={<Storage />} />
          <Route path="trash" element={<Trash />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<Dashboard />} />
        </Route>
      </Routes>
    </Suspense>
  )
}

function App() {
  const [authed, setAuthed] = useState<boolean | null>(null)
  const me = useQuery({
    queryKey: ['me'],
    queryFn: () => get<User>('/auth/me'),
    retry: false,
    staleTime: Infinity,
  })
  useEffect(() => {
    setUnauthorizedHandler(() => {
      queryClient.clear()
      setAuthed(false)
    })
  }, [])
  useEffect(() => {
    if (me.isSuccess) setAuthed(true)
    if (me.isError) setAuthed(me.error instanceof ApiError && me.error.status === 401 ? false : null)
  }, [me.isSuccess, me.isError, me.error])

  if (authed === null && me.isError) {
    return (
      <div className="center" style={{ minHeight: '100dvh' }}>
        <div className="empty">
          <h3>Сервер недоступен</h3>
          <button className="btn" onClick={() => me.refetch()}>
            Повторить
          </button>
        </div>
      </div>
    )
  }
  if (authed === null) return <Loading />
  if (!authed) return <Login onLogin={() => setAuthed(true)} />
  return <Authed />
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <ConfirmProvider>
          <App />
          <Toaster position="top-center" richColors closeButton toastOptions={{ style: { fontFamily: 'var(--font)' } }} />
        </ConfirmProvider>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)

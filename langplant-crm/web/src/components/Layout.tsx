import clsx from 'clsx'
import {
  CalendarDays,
  ChevronDown,
  ChevronLeft,
  Clapperboard,
  HardDrive,
  History,
  LayoutDashboard,
  LogOut,
  Menu as MenuIcon,
  Monitor,
  Moon,
  Music,
  Plus,
  Settings,
  Sprout,
  Sun,
  Trash,
} from 'lucide-react'
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { post } from '../lib/api'
import { usePlayer } from '../lib/player'
import { useUndoShortcuts } from '../lib/undo'
import { queryClient, useDashboard, useDicts } from '../lib/queries'
import { NewVideoModal } from '../pages/NewVideo'
import { PlayerBar } from './PlayerBar'
import { UndoDock, UndoHistory, UndoTopButton } from './UndoCenter'
import { UploadPanel } from './UploadPanel'
import { Avatar, IconButton, Menu, MenuItem, MenuLabel, MenuSep, Modal } from './ui'

type Theme = 'system' | 'light' | 'dark'
export function applyTheme(t: Theme) {
  if (t === 'system') document.documentElement.removeAttribute('data-theme')
  else document.documentElement.setAttribute('data-theme', t)
  try {
    localStorage.setItem('crm.theme', t)
  } catch {
    /* ignore */
  }
}
export const savedTheme = (): Theme => {
  try {
    return (localStorage.getItem('crm.theme') as Theme) || 'system'
  } catch {
    return 'system'
  }
}

const NewVideoCtx = createContext<(planDate?: string) => void>(() => {})
export const useNewVideo = () => useContext(NewVideoCtx)

const TITLES: [RegExp, string][] = [
  [/^\/$/, 'Сводка'],
  [/^\/videos\/\d+/, 'Ролик'],
  [/^\/videos/, 'Ролики'],
  [/^\/calendar/, 'Календарь'],
  [/^\/music/, 'Музыка'],
  [/^\/storage/, 'Хранилище'],
  [/^\/trash/, 'Корзина'],
  [/^\/settings/, 'Настройки'],
]

export function Layout() {
  const d = useDicts()
  const loc = useLocation()
  const nav = useNavigate()
  const player = usePlayer()
  const dash = useDashboard()
  const [newVideo, setNewVideo] = useState<{ open: boolean; date?: string }>({ open: false })
  const [more, setMore] = useState(false)
  const [history, setHistory] = useState(false)
  const [theme, setTheme] = useState<Theme>(savedTheme())
  const online = dash.data?.storage.online
  const title = TITLES.find(([re]) => re.test(loc.pathname))?.[1] ?? 'LangPlant'
  // detail pages get a back button in the phone top bar instead of the logo
  const detail = /^\/videos\/\d+/.test(loc.pathname) ? '/videos' : null

  useEffect(() => {
    document.documentElement.style.setProperty('--player', player.track ? '68px' : '0px')
  }, [player.track])
  useEffect(() => setMore(false), [loc.pathname])
  useUndoShortcuts()

  const logout = async () => {
    await post('/auth/logout').catch(() => {})
    queryClient.clear()
    window.location.href = '/'
  }
  const changeTheme = (t: Theme) => {
    setTheme(t)
    applyTheme(t)
  }

  const links = [
    { to: '/', icon: <LayoutDashboard size={18} />, label: 'Сводка', end: true },
    { to: '/videos', icon: <Clapperboard size={18} />, label: 'Ролики' },
    { to: '/calendar', icon: <CalendarDays size={18} />, label: 'Календарь' },
    { to: '/music', icon: <Music size={18} />, label: 'Музыка' },
  ]
  const secondary = [
    { to: '/storage', icon: <HardDrive size={18} />, label: 'Хранилище' },
    { to: '/trash', icon: <Trash size={18} />, label: 'Корзина' },
    { to: '/settings', icon: <Settings size={18} />, label: 'Настройки' },
  ]

  return (
    <NewVideoCtx.Provider value={(date) => setNewVideo({ open: true, date })}>
      <div className="app">
        <aside className="sidebar">
          <div className="brand">
            <span className="brand-logo">
              <Sprout size={17} />
            </span>
            <div>
              LangPlant
              <small>Контент-CRM</small>
            </div>
          </div>
          <button className="btn primary" style={{ margin: '0 2px 14px' }} onClick={() => setNewVideo({ open: true })}>
            <Plus size={16} /> Новый ролик
          </button>
          <nav className="nav">
            {links.map((l) => (
              <NavLink key={l.to} to={l.to} end={l.end} className={({ isActive }) => clsx('nav-link', isActive && 'active')}>
                {l.icon}
                {l.label}
              </NavLink>
            ))}
            <div className="nav-sep" />
            {secondary.map((l) => (
              <NavLink key={l.to} to={l.to} className={({ isActive }) => clsx('nav-link', isActive && 'active')}>
                {l.icon}
                {l.label}
                {l.to === '/storage' && online !== undefined && <span className={clsx('dot', online ? 'on' : 'off')} style={{ marginLeft: 'auto' }} title={online ? 'Хранилище онлайн' : 'Хранилище офлайн'} />}
              </NavLink>
            ))}
          </nav>
          <div className="sidebar-foot">
            <UndoDock onHistory={() => setHistory(true)} />
            <Menu
              align="start"
              trigger={
                <button className="nav-link" style={{ border: 0, background: 'none', width: '100%', cursor: 'pointer' }}>
                  <Avatar user={d.me} />
                  <span className="grow ellipsis" style={{ textAlign: 'left' }}>
                    {d.me.name}
                  </span>
                  <ChevronDown size={15} />
                </button>
              }
            >
              <MenuLabel>Тема</MenuLabel>
              <MenuItem icon={<Monitor size={15} />} onSelect={() => changeTheme('system')} right={theme === 'system' ? '✓' : undefined}>
                Как в системе
              </MenuItem>
              <MenuItem icon={<Sun size={15} />} onSelect={() => changeTheme('light')} right={theme === 'light' ? '✓' : undefined}>
                Светлая
              </MenuItem>
              <MenuItem icon={<Moon size={15} />} onSelect={() => changeTheme('dark')} right={theme === 'dark' ? '✓' : undefined}>
                Тёмная
              </MenuItem>
              <MenuSep />
              <MenuItem icon={<Settings size={15} />} onSelect={() => nav('/settings')}>
                Профиль и настройки
              </MenuItem>
              <MenuItem icon={<LogOut size={15} />} onSelect={logout} danger>
                Выйти
              </MenuItem>
            </Menu>
          </div>
        </aside>

        <header className="topbar">
          {detail ? (
            <IconButton label="Назад" className="back" onClick={() => (window.history.state?.idx > 0 ? nav(-1) : nav(detail))}>
              <ChevronLeft size={22} />
            </IconButton>
          ) : (
            <span className="brand-logo" style={{ width: 26, height: 26 }}>
              <Sprout size={15} />
            </span>
          )}
          <div className="title">{title}</div>
          <UndoTopButton onHistory={() => setHistory(true)} />
          {online !== undefined && (
            <NavLink to="/storage" className="icon-btn" aria-label="Хранилище">
              <span className={clsx('dot', online ? 'on' : 'off')} />
            </NavLink>
          )}
          <NavLink to="/settings" aria-label="Профиль">
            <Avatar user={d.me} />
          </NavLink>
        </header>

        <main className="main">
          <Outlet />
        </main>

        <nav className="bottombar">
          {links.map((l) => (
            <NavLink key={l.to} to={l.to} end={l.end} className={({ isActive }) => clsx(isActive && 'active')}>
              {l.icon}
              {l.label}
            </NavLink>
          ))}
          <button onClick={() => setMore(true)} className={clsx(secondary.some((s) => loc.pathname.startsWith(s.to)) && 'active')}>
            <MenuIcon size={18} />
            Ещё
          </button>
        </nav>

        <Modal open={more} onClose={() => setMore(false)} title="Ещё">
          <div className="nav" style={{ gap: 4 }}>
            {secondary.map((l) => (
              <NavLink key={l.to} to={l.to} className="nav-link" style={{ padding: '12px 10px', fontSize: 15 }}>
                {l.icon}
                {l.label}
                {l.to === '/storage' && online !== undefined && <span className={clsx('dot', online ? 'on' : 'off')} style={{ marginLeft: 'auto' }} />}
              </NavLink>
            ))}
            <button
              className="nav-link"
              style={{ padding: '12px 10px', fontSize: 15, border: 0, background: 'none', width: '100%' }}
              onClick={() => {
                setMore(false)
                setHistory(true)
              }}
            >
              <History size={18} /> История изменений
            </button>
            <div className="nav-sep" />
            <div className="row" style={{ padding: '6px 10px', justifyContent: 'space-between' }}>
              <span className="text-2">Тема</span>
              <div className="seg">
                {(
                  [
                    ['system', <Monitor size={15} key="m" />],
                    ['light', <Sun size={15} key="s" />],
                    ['dark', <Moon size={15} key="d" />],
                  ] as [Theme, ReactNode][]
                ).map(([t, icon]) => (
                  <button key={t} className={clsx(theme === t && 'on')} onClick={() => changeTheme(t)} aria-label={t}>
                    {icon}
                  </button>
                ))}
              </div>
            </div>
            <button className="nav-link" style={{ padding: '12px 10px', fontSize: 15, border: 0, background: 'none', color: 'var(--red-text)' }} onClick={logout}>
              <LogOut size={18} /> Выйти
            </button>
          </div>
        </Modal>

        {(loc.pathname === '/videos' || loc.pathname === '/') && (
          <button className="fab" aria-label="Новый ролик" onClick={() => setNewVideo({ open: true })}>
            <Plus size={24} />
          </button>
        )}
        <UndoHistory open={history} onClose={() => setHistory(false)} />
        <UploadPanel />
        <PlayerBar />
        <NewVideoModal open={newVideo.open} planDate={newVideo.date} onClose={() => setNewVideo({ open: false })} />
      </div>
    </NewVideoCtx.Provider>
  )
}

export function PageHead({ title, sub, actions }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-head">
      <div className="hide-m">
        <h1>{title}</h1>
        {sub && <div className="sub">{sub}</div>}
      </div>
      {sub && <div className="sub hide-d">{sub}</div>}
      {actions && <div className="actions">{actions}</div>}
    </div>
  )
}


import * as Dialog from '@radix-ui/react-dialog'
import * as Dropdown from '@radix-ui/react-dropdown-menu'
import * as Pop from '@radix-ui/react-popover'
import clsx from 'clsx'
import { Clapperboard, X } from 'lucide-react'
import { createContext, forwardRef, useCallback, useContext, useId, useLayoutEffect, useMemo, useRef, useState, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { derivedUrl } from '../lib/api'
import { initials } from '../lib/format'
import type { Platform, Stage, User } from '../lib/types'

type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'accent' | 'ghost' | 'danger' | 'default'
  size?: 'sm' | 'lg'
  block?: boolean
  icon?: ReactNode
  loading?: boolean
}

export const Button = forwardRef<HTMLButtonElement, BtnProps>(function Button(
  { variant = 'default', size, block, icon, loading, className, children, disabled, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      className={clsx('btn', variant !== 'default' && variant, size, block && 'block', className)}
      disabled={disabled || loading}
      {...rest}
    >
      {loading ? <span className="spinner" style={{ width: 14, height: 14 }} /> : icon}
      {children}
    </button>
  )
})

export const IconButton = forwardRef<HTMLButtonElement, ButtonHTMLAttributes<HTMLButtonElement> & { size?: 'sm'; active?: boolean; label: string }>(
  function IconButton({ size, active, label, className, children, ...rest }, ref) {
    return (
      <button ref={ref} className={clsx('icon-btn', size, active && 'active', className)} aria-label={label} title={label} {...rest}>
        {children}
      </button>
    )
  },
)

export function Spinner({ size = 18 }: { size?: number }) {
  return <span className="spinner" style={{ width: size, height: size }} />
}

export function Loading() {
  return (
    <div className="center">
      <Spinner size={22} />
    </div>
  )
}

export function Empty({ icon, title, children, action }: { icon?: ReactNode; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      {icon && <div className="icon">{icon}</div>}
      <h3>{title}</h3>
      {children && <div className="small">{children}</div>}
      {action && <div style={{ marginTop: 8 }}>{action}</div>}
    </div>
  )
}

export const avatarUrl = (u: Pick<User, 'id' | 'avatar_at'>) => `/api/users/${u.id}/avatar?v=${u.avatar_at}`

export function Avatar({ user, size }: { user?: User | null; size?: 'sm' | 'lg' | 'xl' }) {
  const [broken, setBroken] = useState<string | null>(null)
  if (!user) return <span className={clsx('avatar', size)} style={{ background: 'var(--surface-3)', color: 'var(--text-3)' }} />
  const src = user.avatar_at ? avatarUrl(user) : null
  return (
    <span className={clsx('avatar', size)} style={{ background: user.color }} title={user.name}>
      {src && broken !== src ? <img src={src} alt="" loading="lazy" onError={() => setBroken(src)} /> : initials(user.name)}
    </span>
  )
}

export function Toggle({ on, onChange, disabled, label }: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean; label?: string }) {
  return <button type="button" role="switch" aria-checked={on} aria-label={label} className={clsx('toggle', on && 'on')} disabled={disabled} onClick={() => onChange(!on)} />
}

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
      {hint && <div className="hint">{hint}</div>}
    </div>
  )
}

export function Seg<T extends string>({ value, onChange, options }: { value: T; onChange: (v: T) => void; options: { value: T; label: ReactNode; title?: string }[] }) {
  return (
    <div className="seg" role="tablist">
      {options.map((o) => (
        <button key={o.value} type="button" className={clsx(o.value === value && 'on')} onClick={() => onChange(o.value)} title={o.title} role="tab" aria-selected={o.value === value}>
          {o.label}
        </button>
      ))}
    </div>
  )
}

export function Progress({ value, tone }: { value: number; tone?: 'amber' | 'red' | 'blue' }) {
  return (
    <div className={clsx('progress', tone)}>
      <div style={{ width: `${Math.max(0, Math.min(100, value * 100))}%` }} />
    </div>
  )
}

export function StagePill({ stage, size }: { stage?: Stage | null; size?: 'sm' }) {
  if (!stage) return <span className={clsx('chip', size)}>Без этапа</span>
  return (
    <span className={clsx('chip', size)} style={{ background: `color-mix(in srgb, ${stage.color} 16%, transparent)`, color: `color-mix(in srgb, ${stage.color} 75%, var(--text))` }}>
      <span className="dot" style={{ background: stage.color }} />
      {stage.name}
    </span>
  )
}

const brandPaths: Record<string, ReactNode> = {
  tiktok: <path fill="currentColor" d="M16.6 5.8A4.3 4.3 0 0 1 15.5 3h-3.1v12.4a2.6 2.6 0 1 1-2.6-2.6c.3 0 .5 0 .8.1V9.7a5.7 5.7 0 1 0 4.9 5.7V9.1a7.3 7.3 0 0 0 4.3 1.4V7.4a4.3 4.3 0 0 1-3.2-1.6Z" />,
  youtube: <path fill="currentColor" d="M21.6 7.2a2.5 2.5 0 0 0-1.8-1.8C18.2 5 12 5 12 5s-6.2 0-7.8.4A2.5 2.5 0 0 0 2.4 7.2 26 26 0 0 0 2 12a26 26 0 0 0 .4 4.8 2.5 2.5 0 0 0 1.8 1.8C5.8 19 12 19 12 19s6.2 0 7.8-.4a2.5 2.5 0 0 0 1.8-1.8A26 26 0 0 0 22 12a26 26 0 0 0-.4-4.8ZM10 15V9l5.2 3L10 15Z" />,
  instagram: (
    <g fill="none" stroke="currentColor" strokeWidth="2.2">
      <rect x="3" y="3" width="18" height="18" rx="5" />
      <circle cx="12" cy="12" r="4" />
      <circle cx="17.5" cy="6.5" r="0.6" fill="currentColor" />
    </g>
  ),
  vk: <path fill="currentColor" d="M12.8 17.5c-5.5 0-8.6-3.8-8.7-10h2.7c.1 4.6 2.1 6.5 3.7 6.9V7.5h2.6v3.9c1.6-.2 3.2-2 3.8-3.9h2.6a7.6 7.6 0 0 1-3.5 5 7.9 7.9 0 0 1 4.1 5h-2.8a5 5 0 0 0-4.2-3.6v3.6h-.3Z" />,
  telegram: <path fill="currentColor" d="m20.7 4.3-17.6 6.8c-1.2.5-1.2 1.2-.2 1.5l4.5 1.4 1.7 5.3c.2.6.4.8.8.8s.6-.2.9-.5l2.2-2.1 4.6 3.4c.8.5 1.4.2 1.6-.8l2.9-13.9c.3-1.3-.5-1.8-1.4-1.4ZM9.4 14l8.5-5.4c.4-.3.8-.1.5.2l-7 6.4-.3 3.1L9.4 14Z" />,
}

export function PlatformIcon({ platform, size }: { platform?: Platform; size?: 'sm' }) {
  if (!platform) return <span className={clsx('picon', size)} style={{ background: 'var(--surface-3)' }} />
  const path = brandPaths[platform.icon]
  return (
    <span className={clsx('picon', size)} style={{ background: platform.color }} title={platform.name}>
      {path ? (
        <svg viewBox="0 0 24 24" aria-hidden>
          {path}
        </svg>
      ) : (
        platform.name.slice(0, 1).toUpperCase()
      )}
    </span>
  )
}
export const PLATFORM_ICONS = Object.keys(brandPaths)

export function Thumb({ sha, size, className }: { sha: string | null | undefined; size?: 'sm' | 'lg'; className?: string }) {
  const [failed, setFailed] = useState(false)
  return (
    <div className={clsx('thumb', size, className)}>
      {sha && !failed ? <img src={derivedUrl(sha, 'thumb.jpg')} alt="" loading="lazy" onError={() => setFailed(true)} /> : <Clapperboard size={size === 'sm' ? 14 : 18} />}
    </div>
  )
}

/**
 * Audio waveform. With onSeek it is a scrubber: click to jump, press and drag
 * to move through the track (the bars and a time label follow the pointer,
 * onScrub gets live positions), arrow keys step by 5 seconds.
 */
export function Waveform({
  peaks,
  progress = 0,
  onSeek,
  onScrub,
  durationMs,
  height = 28,
  barWidth = 2,
  className,
  label = 'Позиция в треке',
}: {
  peaks: number[] | null
  progress?: number
  onSeek?: (f: number) => void
  onScrub?: (f: number) => void
  durationMs?: number | null
  height?: number
  barWidth?: number
  className?: string
  label?: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  const [hover, setHover] = useState<number | null>(null)
  const [drag, setDrag] = useState<number | null>(null)
  const clip = 'w' + useId().replace(/[^a-zA-Z0-9_-]/g, '')
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    setWidth(el.clientWidth)
    const ro = new ResizeObserver(() => setWidth(el.clientWidth))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  const step = barWidth + 1
  const bars = Math.max(16, Math.floor((width || 240) / step))
  const data = useMemo(() => resample(peaks, bars), [peaks, bars])
  const w = bars * step
  const shown = drag ?? progress
  const frac = (e: React.PointerEvent) => {
    const r = e.currentTarget.getBoundingClientRect()
    return Math.max(0, Math.min(1, (e.clientX - r.left) / r.width))
  }
  const interactive = !!onSeek
  const tip = drag ?? hover
  const fmtTip = (f: number) => {
    const s = Math.round((f * (durationMs ?? 0)) / 1000)
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
  }
  const bars$ = data.map((v, i) => {
    const h = Math.max(2, (v / 100) * height)
    return <rect key={i} x={i * step} y={(height - h) / 2} width={barWidth} height={h} rx={barWidth / 2} />
  })
  return (
    <div
      ref={ref}
      className={clsx('wave', interactive && 'seekable', drag != null && 'dragging', className)}
      style={{ height }}
      role={interactive ? 'slider' : undefined}
      tabIndex={interactive ? 0 : undefined}
      aria-label={interactive ? label : undefined}
      aria-valuemin={interactive ? 0 : undefined}
      aria-valuemax={interactive ? 100 : undefined}
      aria-valuenow={interactive ? Math.round(shown * 100) : undefined}
      onPointerDown={(e) => {
        if (!interactive || e.button > 0) return
        e.preventDefault()
        e.currentTarget.setPointerCapture(e.pointerId)
        const f = frac(e)
        setDrag(f)
        onScrub?.(f)
      }}
      onPointerMove={(e) => {
        if (!interactive) return
        const f = frac(e)
        if (drag != null) {
          setDrag(f)
          onScrub?.(f)
        } else if (e.pointerType === 'mouse') setHover(f)
      }}
      onPointerUp={(e) => {
        if (drag == null) return
        onSeek?.(frac(e))
        setDrag(null)
      }}
      onPointerCancel={() => setDrag(null)}
      onPointerLeave={() => setHover(null)}
      onKeyDown={(e) => {
        if (!interactive || (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight')) return
        e.preventDefault()
        const d = durationMs ? 5000 / durationMs : 0.05
        onSeek?.(Math.max(0, Math.min(1, progress + (e.key === 'ArrowRight' ? d : -d))))
      }}
    >
      <svg viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" aria-hidden>
        <defs>
          <clipPath id={`${clip}p`}>
            <rect width={shown * w} height={height} />
          </clipPath>
          {tip != null && (
            <clipPath id={`${clip}h`}>
              <rect width={tip * w} height={height} />
            </clipPath>
          )}
        </defs>
        <g className="base">{bars$}</g>
        {tip != null && drag == null && (
          <g className="hover" clipPath={`url(#${clip}h)`}>
            {bars$}
          </g>
        )}
        <g className="played" clipPath={`url(#${clip}p)`}>
          {bars$}
        </g>
      </svg>
      {interactive && <span className="wave-head" style={{ left: `${shown * 100}%` }} />}
      {interactive && tip != null && !!durationMs && (
        <span className="wave-tip" style={{ left: `${tip * 100}%` }}>
          {fmtTip(tip)}
        </span>
      )}
    </div>
  )
}

function resample(peaks: number[] | null, n: number): number[] {
  if (!peaks || peaks.length === 0) return Array.from({ length: n }, (_, i) => 18 + 10 * Math.sin(i / 2.3) * Math.cos(i / 5.1))
  const out: number[] = []
  for (let i = 0; i < n; i++) {
    // more bars than data: stretch; fewer: the loudest value of each group
    const from = Math.floor((i * peaks.length) / n)
    const to = Math.max(from + 1, Math.floor(((i + 1) * peaks.length) / n))
    let m = 0
    for (let j = from; j < to; j++) m = Math.max(m, peaks[j] ?? 0)
    out.push(m)
  }
  return out
}

// ---- dialogs ---------------------------------------------------------------

// Popovers and menus opened inside a dialog render into it: a dialog locks
// scrolling outside itself, so a popover portaled to <body> could not scroll.
const PortalCtx = createContext<HTMLElement | null>(null)

export function Modal({
  open,
  onClose,
  title,
  children,
  footer,
  wide,
}: {
  open: boolean
  onClose: () => void
  title: ReactNode
  children: ReactNode
  footer?: ReactNode
  wide?: boolean
}) {
  const [el, setEl] = useState<HTMLDivElement | null>(null)
  return (
    <Dialog.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="overlay" />
        <Dialog.Content
          ref={setEl}
          className={clsx('modal', wide && 'wide')}
          aria-describedby={undefined}
          // without an autoFocus field, focus the dialog itself: no focus ring on the close button, no keyboard popping up on phones
          onOpenAutoFocus={(e) => {
            e.preventDefault()
            ;(e.currentTarget as HTMLElement).focus({ preventScroll: true })
          }}
        >
          <PortalCtx.Provider value={el}>
          <div className="modal-head">
            <Dialog.Title asChild>
              <h2>{title}</h2>
            </Dialog.Title>
            <Dialog.Close asChild>
              <IconButton label="Закрыть">
                <X size={18} />
              </IconButton>
            </Dialog.Close>
          </div>
          <div className="modal-body">{children}</div>
          {footer && <div className="modal-foot">{footer}</div>}
          </PortalCtx.Provider>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

interface ConfirmOpts {
  title: string
  text?: ReactNode
  confirm?: string
  danger?: boolean
}
const ConfirmCtx = createContext<(o: ConfirmOpts) => Promise<boolean>>(async () => false)
export const useConfirm = () => useContext(ConfirmCtx)

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<(ConfirmOpts & { resolve: (v: boolean) => void }) | null>(null)
  const confirm = useCallback((o: ConfirmOpts) => new Promise<boolean>((resolve) => setState({ ...o, resolve })), [])
  const close = (v: boolean) => {
    state?.resolve(v)
    setState(null)
  }
  return (
    <ConfirmCtx.Provider value={confirm}>
      {children}
      <Modal
        open={!!state}
        onClose={() => close(false)}
        title={state?.title}
        footer={
          <>
            <Button onClick={() => close(false)}>Отмена</Button>
            <Button variant={state?.danger ? 'danger' : 'primary'} className={state?.danger ? 'solid' : undefined} onClick={() => close(true)} autoFocus>
              {state?.confirm ?? 'Подтвердить'}
            </Button>
          </>
        }
      >
        {state?.text && <div className="text-2">{state.text}</div>}
      </Modal>
    </ConfirmCtx.Provider>
  )
}

// ---- menus & popovers --------------------------------------------------------

export function Menu({ trigger, children, align = 'end' }: { trigger: ReactNode; children: ReactNode; align?: 'start' | 'end' | 'center' }) {
  const container = useContext(PortalCtx) ?? undefined
  return (
    <Dropdown.Root modal={false}>
      <Dropdown.Trigger asChild>{trigger}</Dropdown.Trigger>
      <Dropdown.Portal container={container}>
        <Dropdown.Content className="popover" align={align} sideOffset={6} collisionPadding={10}>
          {children}
        </Dropdown.Content>
      </Dropdown.Portal>
    </Dropdown.Root>
  )
}

export function MenuItem({ icon, children, onSelect, danger, right }: { icon?: ReactNode; children: ReactNode; onSelect?: () => void; danger?: boolean; right?: ReactNode }) {
  return (
    <Dropdown.Item className={clsx('menu-item', danger && 'danger')} onSelect={onSelect}>
      {icon}
      <span className="grow ellipsis">{children}</span>
      {right && <span className="right">{right}</span>}
    </Dropdown.Item>
  )
}
export const MenuSep = () => <Dropdown.Separator className="menu-sep" />
export const MenuLabel = ({ children }: { children: ReactNode }) => <Dropdown.Label className="menu-label">{children}</Dropdown.Label>

export function Popover({
  trigger,
  children,
  open,
  onOpenChange,
  pad,
  align = 'start',
}: {
  trigger: ReactNode
  children: ReactNode
  open?: boolean
  onOpenChange?: (o: boolean) => void
  pad?: boolean
  align?: 'start' | 'end' | 'center'
}) {
  const container = useContext(PortalCtx) ?? undefined
  return (
    <Pop.Root open={open} onOpenChange={onOpenChange}>
      <Pop.Trigger asChild>{trigger}</Pop.Trigger>
      <Pop.Portal container={container}>
        <Pop.Content className={clsx('popover', pad && 'pad')} align={align} sideOffset={6} collisionPadding={10}>
          {children}
        </Pop.Content>
      </Pop.Portal>
    </Pop.Root>
  )
}
export const PopoverClose = Pop.Close

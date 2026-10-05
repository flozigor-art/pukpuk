import * as Dialog from '@radix-ui/react-dialog'
import * as Dropdown from '@radix-ui/react-dropdown-menu'
import * as Pop from '@radix-ui/react-popover'
import clsx from 'clsx'
import { Clapperboard, X } from 'lucide-react'
import { createContext, forwardRef, useCallback, useContext, useState, type ButtonHTMLAttributes, type ReactNode } from 'react'
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

export function Avatar({ user, size }: { user?: User | null; size?: 'sm' | 'lg' }) {
  if (!user) return <span className={clsx('avatar', size)} style={{ background: 'var(--surface-3)', color: 'var(--text-3)' }} />
  return (
    <span className={clsx('avatar', size)} style={{ background: user.color }} title={user.name}>
      {initials(user.name)}
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

export function Waveform({ peaks, progress = 0, onSeek, bars = 80, height = 28 }: { peaks: number[] | null; progress?: number; onSeek?: (f: number) => void; bars?: number; height?: number }) {
  const data = resample(peaks, bars)
  const w = bars * 3
  return (
    <svg
      className="wave"
      viewBox={`0 0 ${w} ${height}`}
      preserveAspectRatio="none"
      style={{ height }}
      onClick={(e) => {
        if (!onSeek) return
        const r = e.currentTarget.getBoundingClientRect()
        onSeek((e.clientX - r.left) / r.width)
      }}
    >
      {data.map((v, i) => {
        const h = Math.max(2, (v / 100) * height)
        return <rect key={i} x={i * 3} y={(height - h) / 2} width={2} height={h} rx={1} className={i / data.length < progress ? 'p' : undefined} />
      })}
    </svg>
  )
}

function resample(peaks: number[] | null, n: number): number[] {
  if (!peaks || peaks.length === 0) return Array.from({ length: n }, (_, i) => 18 + 10 * Math.sin(i / 2.3) * Math.cos(i / 5.1))
  const out: number[] = []
  for (let i = 0; i < n; i++) {
    const from = Math.floor((i * peaks.length) / n)
    const to = Math.max(from + 1, Math.floor(((i + 1) * peaks.length) / n))
    let m = 0
    for (let j = from; j < to; j++) m = Math.max(m, peaks[j] ?? 0)
    out.push(m)
  }
  return out
}

// ---- dialogs ---------------------------------------------------------------

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
  return (
    <Dialog.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="overlay" />
        <Dialog.Content className={clsx('modal', wide && 'wide')} aria-describedby={undefined}>
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
  return (
    <Dropdown.Root modal={false}>
      <Dropdown.Trigger asChild>{trigger}</Dropdown.Trigger>
      <Dropdown.Portal>
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
  return (
    <Pop.Root open={open} onOpenChange={onOpenChange}>
      <Pop.Trigger asChild>{trigger}</Pop.Trigger>
      <Pop.Portal>
        <Pop.Content className={clsx('popover', pad && 'pad')} align={align} sideOffset={6} collisionPadding={10}>
          {children}
        </Pop.Content>
      </Pop.Portal>
    </Pop.Root>
  )
}
export const PopoverClose = Pop.Close

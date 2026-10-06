import clsx from 'clsx'
import { History, Lock, Redo2, Undo2 } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'
import { dayOf, fmtAgo, fmtDay, fmtTime, relDay, today } from '../lib/format'
import { useDicts, useUndoHistory } from '../lib/queries'
import type { UndoItem } from '../lib/types'
import { baseLabel, revert, undoKeys } from '../lib/undo'
import { Avatar, Button, IconButton, Modal, Spinner } from './ui'

/** The user's own latest change that can still be undone. */
function useMyLast(): UndoItem | undefined {
  const d = useDicts()
  const { data } = useUndoHistory()
  return data?.find((i) => i.user_id === d.me.id && i.kind !== 'undo' && !i.undone)
}

/** Desktop: always visible at the bottom of the sidebar. */
export function UndoDock({ onHistory }: { onHistory: () => void }) {
  const last = useMyLast()
  const [busy, setBusy] = useState(false)
  const undo = async () => {
    if (!last) return
    if (last.blocked) return onHistory() // changed later: the history explains what to undo first
    setBusy(true)
    await revert(last.id)
    setBusy(false)
  }
  return (
    <div className="undo-dock">
      <button className="undo-main" onClick={undo} disabled={!last || busy} title={last ? `Отменить: ${last.label} (${undoKeys})` : 'Нечего отменять'}>
        {busy ? <Spinner size={15} /> : <Undo2 size={16} />}
        <span className="grow" style={{ minWidth: 0 }}>
          <span className="undo-title">
            Отменить <span className="kbd">{undoKeys}</span>
          </span>
          <span className="undo-sub ellipsis">{!last ? 'Ваших изменений пока нет' : last.blocked ? 'Изменено позже — открыть историю' : last.label}</span>
        </span>
      </button>
      <IconButton label="История изменений" onClick={onHistory}>
        <History size={16} />
      </IconButton>
    </div>
  )
}

/** Phone: a button in the top bar. */
export function UndoTopButton({ onHistory }: { onHistory: () => void }) {
  return (
    <IconButton label="История изменений" onClick={onHistory}>
      <History size={19} />
    </IconButton>
  )
}

export function UndoHistory({ open, onClose }: { open: boolean; onClose: () => void }) {
  const d = useDicts()
  const { data, isLoading } = useUndoHistory(open)
  const last = useMyLast()
  const [pending, setPending] = useState<number | null>(null)
  const [flash, setFlash] = useState<number | null>(null)
  const act = async (id: number) => {
    setPending(id)
    await revert(id)
    setPending(null)
  }
  const show = (id: number) => {
    document.getElementById(`undo-row-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    setFlash(id)
    window.setTimeout(() => setFlash(null), 1600)
  }
  const blockerName = (it: UndoItem) => {
    const b = data?.find((x) => x.id === it.blocked_by)
    return b ? `${b.kind === 'action' ? b.label : baseLabel(b.label)} (${b.user_id ? d.userById.get(b.user_id)?.name ?? '' : ''})` : ''
  }

  const groups: { day: string; items: UndoItem[] }[] = []
  for (const it of data ?? []) {
    const day = dayOf(it.updated_at)
    const g = groups[groups.length - 1]
    if (g?.day === day) g.items.push(it)
    else groups.push({ day, items: [it] })
  }
  const dayTitle = (day: string) => {
    const r = relDay(day)
    return day === today() || r === 'вчера' ? r[0].toUpperCase() + r.slice(1) : fmtDay(day, { weekday: true })
  }

  return (
    <Modal open={open} onClose={onClose} title="История изменений">
      <div className="undo-hero">
        {last ? (
          <>
            <div className="grow" style={{ minWidth: 0 }}>
              <div className="small muted">Ваше последнее изменение · {fmtAgo(last.updated_at)}</div>
              <div className="undo-hero-label">{last.label}</div>
            </div>
            {last.blocked ? (
              <Button onClick={() => last.blocked_by && show(last.blocked_by)} disabled={!last.blocked_by}>
                Что мешает?
              </Button>
            ) : (
              <Button variant="primary" icon={<Undo2 size={16} />} loading={pending === last.id} onClick={() => act(last.id)}>
                Отменить
              </Button>
            )}
          </>
        ) : (
          <div className="small muted">Ваших изменений, которые можно отменить, пока нет. Ниже — изменения всей команды.</div>
        )}
      </div>

      {isLoading && (
        <div className="center" style={{ padding: 24 }}>
          <Spinner />
        </div>
      )}
      {data && data.length === 0 && <div className="muted small" style={{ padding: '16px 0' }}>Пока ничего не менялось.</div>}

      {groups.map((g) => (
        <div key={g.day}>
          <div className="undo-day">{dayTitle(g.day)}</div>
          {g.items.map((it) => {
            const u = it.user_id ? d.userById.get(it.user_id) : undefined
            const by = it.undone_by_user ? d.userById.get(it.undone_by_user) : undefined
            const locked = it.admin_only && !d.isAdmin
            const isUndo = it.kind === 'undo'
            return (
              <div key={it.id} id={`undo-row-${it.id}`} className={clsx('undo-row', it.undone && 'undone', flash === it.id && 'flash')}>
                <Avatar user={u} size="sm" />
                <div className="grow" style={{ minWidth: 0 }}>
                  <div className="undo-label">
                    {it.kind === 'undo' && <Undo2 size={13} className="undo-kind" />}
                    {it.kind === 'redo' && <Redo2 size={13} className="undo-kind" />}
                    {it.kind === 'action' ? it.label : `${isUndo ? 'Отменено' : 'Возвращено'}: ${baseLabel(it.label)}`}
                  </div>
                  <div className="undo-meta">
                    {u?.name ?? 'Система'} · {fmtTime(it.updated_at)}
                    {it.video_id && it.video_code && (
                      <>
                        {' · '}
                        <Link to={`/videos/${it.video_id}`} onClick={onClose}>
                          {it.video_code}
                        </Link>
                      </>
                    )}
                    {!it.undone && it.blocked && (
                      <div className="undo-blocked">
                        {it.blocked_by ? (
                          <>
                            Позже это изменили: <button onClick={() => show(it.blocked_by!)}>{blockerName(it) || 'более позднее изменение'}</button>. Сначала отмените его
                          </>
                        ) : (
                          'Данные с тех пор удалены навсегда или изменились — отменить нельзя'
                        )}
                      </div>
                    )}
                    {it.undone && (
                      <span className="undo-undone">
                        {' · '}
                        {isUndo ? 'возвращено' : 'отменено'}
                        {by ? ` (${by.name})` : ''}
                      </span>
                    )}
                  </div>
                </div>
                {!it.undone &&
                  !it.blocked &&
                  (locked ? (
                    <span className="undo-lock" title="Изменения общих настроек может отменить только администратор">
                      <Lock size={14} />
                    </span>
                  ) : (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="undo-btn"
                      icon={isUndo ? <Redo2 size={14} /> : <Undo2 size={14} />}
                      loading={pending === it.id}
                      onClick={() => act(it.id)}
                    >
                      {isUndo ? 'Вернуть' : 'Отменить'}
                    </Button>
                  ))}
              </div>
            )
          })}
        </div>
      ))}
      {data && data.length > 0 && <div className="small muted" style={{ padding: '14px 0 2px' }}>История хранится 30 дней.</div>}
    </Modal>
  )
}

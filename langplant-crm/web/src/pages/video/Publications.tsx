import clsx from 'clsx'
import { Check, ExternalLink, Plus } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'
import { Button, Empty, Field, Modal, PlatformIcon } from '../../components/ui'
import { fmtDateTime, fromLocalInput, toLocalInput } from '../../lib/format'
import { PUB_STATUS } from '../../lib/labels'
import { useAction, useDicts } from '../../lib/queries'
import type { Channel, Publication, PubStatus, VariantSummary, VideoDetail } from '../../lib/types'

export function Publications({ video, variant }: { video: VideoDetail; variant: VariantSummary }) {
  const d = useDicts()
  const act = useAction()
  const [edit, setEdit] = useState<{ channel: Channel; pub?: Publication } | null>(null)
  const pubs = video.publications.filter((p) => p.variant_id === variant.id)
  const channels = d.channels.filter((c) => (!c.archived && (c.language_code === null || c.language_code === variant.lang)) || pubs.some((p) => p.channel_id === c.id))
  const inv = [['video', video.id], ['videos'], ['calendar'], ['dashboard']]

  if (!channels.length)
    return (
      <Empty title="Нет аккаунтов для этого языка">
        Добавьте аккаунты площадок в <Link to="/settings?tab=platforms" style={{ textDecoration: 'underline' }}>настройках</Link>
      </Empty>
    )

  const markPublished = (c: Channel, p?: Publication) =>
    p
      ? act('PATCH', `/publications/${p.id}`, { status: 'published' }, { invalidate: inv })
      : act('POST', '/publications', { variant_id: variant.id, channel_id: c.id, status: 'published' }, { invalidate: inv })

  return (
    <div className="pubgrid">
      {channels.map((c) => {
        const p = pubs.find((x) => x.channel_id === c.id)
        const pl = d.platformById.get(c.platform_id)
        const st = p ? PUB_STATUS[p.status] : null
        const when = p?.status === 'published' ? p.published_at : p?.plan_at
        return (
          <div key={c.id} className="pubrow">
            <div className="platform">
              <PlatformIcon platform={pl} />
              <div style={{ minWidth: 0 }}>
                <div className="ellipsis">{pl?.name}</div>
                <div className="tiny muted ellipsis">
                  {c.name}
                  {c.language_code ? ` · ${c.language_code.toUpperCase()}` : ''}
                </div>
              </div>
            </div>
            <div className="pstat row wrap" style={{ gap: 6 }}>
              {st ? <span className={clsx('chip', st.tone)}>{st.label}</span> : <span className="chip outline">не запланировано</span>}
              {when ? <span className="small text-2">{fmtDateTime(when)}</span> : null}
              {p?.url && (
                <a href={p.url} target="_blank" rel="noreferrer noopener" className="small row" style={{ gap: 3, color: 'var(--blue-text)' }}>
                  <ExternalLink size={12} /> ссылка
                </a>
              )}
              {p?.note && <span className="small muted ellipsis" style={{ maxWidth: 240 }}>{p.note}</span>}
            </div>
            <div className="row" style={{ gap: 4 }}>
              {p?.status !== 'published' && (
                <Button size="sm" icon={<Check size={14} />} onClick={() => markPublished(c, p)} title="Отметить опубликованным сейчас">
                  <span className="hide-m">Вышло</span>
                </Button>
              )}
              <Button size="sm" variant="ghost" onClick={() => setEdit({ channel: c, pub: p })}>
                {p ? 'Изменить' : <><Plus size={14} /> План</>}
              </Button>
            </div>
          </div>
        )
      })}
      {edit && <PublicationModal video={video} variant={variant} channel={edit.channel} pub={edit.pub} onClose={() => setEdit(null)} />}
    </div>
  )
}

function PublicationModal({ video, variant, channel, pub, onClose }: { video: VideoDetail; variant: VariantSummary; channel: Channel; pub?: Publication; onClose: () => void }) {
  const d = useDicts()
  const act = useAction()
  const pl = d.platformById.get(channel.platform_id)
  const [status, setStatus] = useState<PubStatus>(pub?.status ?? 'planned')
  const defaultPlan = pub?.plan_at ?? (video.plan_date ? fromLocalInput(`${video.plan_date}T18:00`) : null)
  const [planAt, setPlanAt] = useState(defaultPlan ? toLocalInput(defaultPlan) : '')
  const [pubAt, setPubAt] = useState(pub?.published_at ? toLocalInput(pub.published_at) : toLocalInput(Date.now()))
  const [url, setUrl] = useState(pub?.url ?? '')
  const [note, setNote] = useState(pub?.note ?? '')
  const [views, setViews] = useState(pub?.views != null ? String(pub.views) : '')
  const inv = [['video', video.id], ['videos'], ['calendar'], ['dashboard']]
  const save = async () => {
    const body = {
      status,
      plan_at: planAt ? fromLocalInput(planAt) : null,
      published_at: status === 'published' ? fromLocalInput(pubAt) : pub?.published_at ?? null,
      url,
      note,
      ...(pub ? { views: views ? Number(views) : null } : {}),
    }
    if (pub) await act('PATCH', `/publications/${pub.id}`, body, { invalidate: inv })
    else await act('POST', '/publications', { ...body, variant_id: variant.id, channel_id: channel.id }, { invalidate: inv })
    onClose()
  }
  const remove = async () => {
    if (pub) await act('DELETE', `/publications/${pub.id}`, undefined, { invalidate: inv })
    onClose()
  }
  return (
    <Modal
      open
      onClose={onClose}
      title={
        <span className="row">
          <PlatformIcon platform={pl} /> {pl?.name} · {channel.name}
        </span>
      }
      footer={
        <>
          {pub && (
            <Button variant="danger" onClick={remove} style={{ marginRight: 'auto' }}>
              Удалить
            </Button>
          )}
          <Button onClick={onClose}>Отмена</Button>
          <Button variant="primary" onClick={save}>
            Сохранить
          </Button>
        </>
      }
    >
      <div className="form">
        <Field label="Статус">
          <div className="chips">
            {(Object.keys(PUB_STATUS) as PubStatus[]).map((s) => (
              <button key={s} type="button" className={clsx('chip', status === s && 'on')} onClick={() => setStatus(s)}>
                {PUB_STATUS[s].label}
              </button>
            ))}
          </div>
        </Field>
        <div className="grid-2">
          <Field label="Дата и время по плану" hint={`Часовой пояс проекта: ${d.settings.timezone}`}>
            <input className="input" type="datetime-local" value={planAt} onChange={(e) => setPlanAt(e.target.value)} />
          </Field>
          {status === 'published' && (
            <Field label="Фактически вышло">
              <input className="input" type="datetime-local" value={pubAt} onChange={(e) => setPubAt(e.target.value)} />
            </Field>
          )}
        </div>
        <Field label="Ссылка на публикацию" hint="Сохраняется в архиве (п.7.5)">
          <input className="input" type="url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://" />
        </Field>
        {pub && (
          <Field label="Просмотры (вручную)">
            <input className="input" inputMode="numeric" value={views} onChange={(e) => setViews(e.target.value.replace(/\D/g, ''))} />
          </Field>
        )}
        <Field label="Заметка">
          <input className="input" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Например: залито с другим описанием" />
        </Field>
      </div>
    </Modal>
  )
}

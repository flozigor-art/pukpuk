import { RotateCcw, Trash as TrashIcon } from 'lucide-react'
import { Link } from 'react-router'
import { PageHead } from '../components/Layout'
import { Button, Empty, Loading, Thumb, useConfirm } from '../components/ui'
import { fmtAgo, fmtBytes } from '../lib/format'
import { useAction, useDicts, useTrash } from '../lib/queries'

export default function Trash() {
  const { data } = useTrash()
  const d = useDicts()
  const act = useAction()
  const confirm = useConfirm()
  if (!data) return <Loading />
  const inv = [['trash'], ['videos'], ['music'], ['dashboard'], ['video']]
  const purge = async (type: string, id: number, name: string) => {
    if (await confirm({ title: 'Удалить навсегда?', text: `«${name}» будет удалён без возможности восстановления. На компе-хранилище файл ещё 30 дней лежит в папке trash/.`, confirm: 'Удалить навсегда', danger: true }))
      act('DELETE', `/trash/${type}/${id}`, undefined, { invalidate: inv, success: 'Удалено' })
  }
  const empty = !data.videos.length && !data.assets.length && !data.tracks.length
  return (
    <div className="page narrow">
      <PageHead title="Корзина" sub={d.isAdmin ? 'Восстановить может любой, удалить навсегда — только администратор' : 'Удалённое можно восстановить. Удалить навсегда может только администратор'} />
      {empty && <Empty icon={<TrashIcon size={22} />} title="Корзина пуста" />}
      {data.videos.length > 0 && (
        <Section title="Ролики">
          {data.videos.map((v) => (
            <div key={v.id} className="list-row">
              <Thumb sha={v.thumb} size="sm" />
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="ellipsis" style={{ fontWeight: 600 }}>
                  <span className="code">{v.code}</span> {v.title}
                </div>
                <div className="tiny muted">
                  {v.asset_count} файлов · удалён {v.deleted_at ? fmtAgo(v.deleted_at) : ''}
                </div>
              </div>
              <Button size="sm" icon={<RotateCcw size={14} />} onClick={() => act('POST', `/videos/${v.id}/restore`, undefined, { invalidate: inv, success: 'Восстановлено' })}>
                <span className="hide-m">Восстановить</span>
              </Button>
              {d.isAdmin && (
                <Button size="sm" variant="danger" onClick={() => purge('video', v.id, v.title)}>
                  <TrashIcon size={14} />
                </Button>
              )}
            </div>
          ))}
        </Section>
      )}
      {data.assets.length > 0 && (
        <Section title="Файлы">
          {data.assets.map((a) => (
            <div key={a.id} className="list-row">
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="ellipsis" style={{ fontWeight: 600 }}>
                  {a.filename}
                </div>
                <div className="tiny muted ellipsis">
                  <Link to={`/videos/${a.video_id}`}>
                    {a.video_code} {a.video_title}
                  </Link>{' '}
                  · {d.kindByKey.get(a.kind)?.name ?? a.kind} · {fmtBytes(a.size)} · удалён {a.deleted_at ? fmtAgo(a.deleted_at) : ''}
                </div>
              </div>
              <Button size="sm" icon={<RotateCcw size={14} />} onClick={() => act('POST', `/assets/${a.id}/restore`, undefined, { invalidate: inv, success: 'Восстановлено' })}>
                <span className="hide-m">Восстановить</span>
              </Button>
              {d.isAdmin && (
                <Button size="sm" variant="danger" onClick={() => purge('asset', a.id, a.filename)}>
                  <TrashIcon size={14} />
                </Button>
              )}
            </div>
          ))}
        </Section>
      )}
      {data.tracks.length > 0 && (
        <Section title="Музыка">
          {data.tracks.map((t) => (
            <div key={t.id} className="list-row">
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="ellipsis" style={{ fontWeight: 600 }}>
                  {t.title}
                </div>
                <div className="tiny muted">
                  {t.artist || '—'} · {fmtBytes(t.size)}
                </div>
              </div>
              <Button size="sm" icon={<RotateCcw size={14} />} onClick={() => act('POST', `/music/${t.id}/restore`, undefined, { invalidate: inv, success: 'Восстановлено' })}>
                <span className="hide-m">Восстановить</span>
              </Button>
              {d.isAdmin && (
                <Button size="sm" variant="danger" onClick={() => purge('track', t.id, t.title)}>
                  <TrashIcon size={14} />
                </Button>
              )}
            </div>
          ))}
        </Section>
      )}
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <>
      <div className="section-title">
        <h2>{title}</h2>
      </div>
      <div className="card card-pad" style={{ paddingTop: 6, paddingBottom: 6 }}>
        <div className="list">{children}</div>
      </div>
    </>
  )
}

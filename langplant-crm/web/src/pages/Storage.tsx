import clsx from 'clsx'
import { Cloud, Database, HardDrive, RefreshCw, Server, TriangleAlert } from 'lucide-react'
import { Link } from 'react-router'
import { PageHead } from '../components/Layout'
import { Button, Loading, Progress } from '../components/ui'
import { fmtAgo, fmtBytes, fmtDateTime, plural } from '../lib/format'
import { useAction, useDicts, useStorage } from '../lib/queries'
import type { PendingBlob } from '../lib/types'

export default function Storage() {
  const { data } = useStorage()
  const d = useDicts()
  const act = useAction()
  if (!data) return <Loading />
  const n = data.node
  const u = data.usage
  const bufferUsed = u.pinned + u.uploading
  return (
    <div className="page narrow">
      <PageHead
        title="Хранилище"
        sub="Файлы загружаются на сервер и в фоне переносятся на комп — там хранится основная копия"
        actions={
          d.isAdmin && (
            <Button icon={<RefreshCw size={15} />} onClick={() => act('POST', '/storage/resync', undefined, { success: 'Сверка запущена', invalidate: [['storage']] })} disabled={!n.online}>
              Сверить
            </Button>
          )
        }
      />
      <div className="dash-grid">
        <div className="card card-pad span-12">
          <div className="row" style={{ gap: 14, alignItems: 'flex-start' }}>
            <div className="today" style={{ padding: 0 }}>
              <div className="ring" style={{ width: 52, height: 52, background: n.online ? 'var(--accent-soft)' : 'var(--red-soft)', color: n.online ? 'var(--accent)' : 'var(--red)' }}>
                <HardDrive size={24} />
              </div>
            </div>
            <div className="grow">
              <h2 style={{ fontSize: 17 }}>{n.online ? 'Комп-хранилище на связи' : 'Комп-хранилище офлайн'}</h2>
              <div className="small text-2" style={{ marginTop: 3 }}>
                {n.online
                  ? `Подключено ${fmtAgo(n.connected_at)}${n.addr ? ` · ${n.addr}` : ''}`
                  : n.last_seen
                    ? `Последний раз на связи ${fmtAgo(n.last_seen)}. Загрузки продолжают приниматься в буфер сервера.`
                    : 'Нода ещё ни разу не подключалась. Запустите контейнер crm-node на компе.'}
              </div>
              {n.version && (
                <div className="tiny muted" style={{ marginTop: 4 }}>
                  crm-node {n.version} · id {n.node_id} · сервер {data.version}
                </div>
              )}
            </div>
          </div>
          {n.disk_total > 0 && (
            <div style={{ marginTop: 16 }}>
              <div className="row small" style={{ marginBottom: 6 }}>
                <span className="grow text-2">Диск на компе</span>
                <span className="nums muted">
                  свободно {fmtBytes(n.disk_free)} из {fmtBytes(n.disk_total)}
                </span>
              </div>
              <Progress value={1 - n.disk_free / n.disk_total} tone={n.disk_free / n.disk_total < 0.1 ? 'red' : 'blue'} />
              <div className="tiny muted" style={{ marginTop: 6 }}>
                Хранится {n.blobs} {plural(n.blobs, 'файл', 'файла', 'файлов')} ({fmtBytes(n.bytes)})
                {n.queue > 0 && ` · в очереди на скачивание: ${n.queue}`}
                {n.derive > 0 && ` · готовит превью: ${n.derive}`}
              </div>
            </div>
          )}
        </div>

        <Meter className="span-6" icon={<Cloud size={15} />} title="Буфер загрузок на сервере" used={bufferUsed} max={data.limits.buffer}
          foot={data.summary.pending_count > 0 ? `Ждут переноса: ${data.summary.pending_count} (${fmtBytes(data.summary.pending_bytes)})${data.uploads ? ` · загружаются: ${data.uploads}` : ''}` : 'Всё перенесено на комп'} />
        <Meter className="span-6" icon={<Server size={15} />} title="Кэш на сервере" used={u.cached + u.previews} max={data.limits.cache + data.limits.previews}
          foot={`Копии недавно открытых файлов и превью — для быстрого просмотра. Свободно на диске VPS: ${fmtBytes(u.disk_free)}`} />

        <div className="card card-pad span-12">
          <div className="row" style={{ gap: 10 }}>
            <Database size={16} />
            <b className="grow">Резервные копии базы</b>
            <span className="small text-2">{n.last_backup ? `последняя: ${fmtDateTime(n.last_backup)}` : 'ещё не было'}</span>
          </div>
          <div className="small muted" style={{ marginTop: 6 }}>
            Нода регулярно забирает копию базы на комп (папка <span className="mono">backups/</span>). Вместе с файлами этого достаточно для полного восстановления, а команда <span className="mono">crm-node export</span> соберёт из них обычные папки с роликами.
          </div>
        </div>

        {data.missing.length > 0 && (
          <div className="card span-12">
            <div className="card-head">
              <TriangleAlert size={16} color="var(--red)" />
              <h3>Потерянные файлы</h3>
            </div>
            <div className="card-body">
              <div className="small muted" style={{ marginBottom: 8 }}>
                Хранилище не нашло эти файлы. Если у кого-то остались оригиналы — загрузите их заново, связь восстановится автоматически.
              </div>
              <BlobList items={data.missing} />
            </div>
          </div>
        )}
        {data.pending.length > 0 && (
          <div className="card span-12">
            <div className="card-head">
              <Cloud size={16} />
              <h3>Ждут переноса на комп</h3>
            </div>
            <div className="card-body">
              <BlobList items={data.pending} />
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function Meter({ icon, title, used, max, foot, className }: { icon: React.ReactNode; title: string; used: number; max: number; foot: string; className?: string }) {
  const v = max ? used / max : 0
  return (
    <div className={clsx('card card-pad', className)}>
      <div className="row" style={{ marginBottom: 10 }}>
        {icon}
        <b className="grow">{title}</b>
        <span className="small nums muted">
          {fmtBytes(used)} / {fmtBytes(max)}
        </span>
      </div>
      <Progress value={v} tone={v > 0.85 ? 'red' : v > 0.6 ? 'amber' : 'blue'} />
      <div className="tiny muted" style={{ marginTop: 8 }}>
        {foot}
      </div>
    </div>
  )
}

function BlobList({ items }: { items: PendingBlob[] }) {
  return (
    <div className="list">
      {items.map((b) => (
        <div key={b.sha256} className="list-row">
          <div className="grow" style={{ minWidth: 0 }}>
            <div className="ellipsis" style={{ fontWeight: 550 }}>
              {b.video_id ? <Link to={`/videos/${b.video_id}`}>{b.name || b.sha256.slice(0, 12)}</Link> : b.name || b.sha256.slice(0, 12)}
            </div>
            <div className="tiny muted">
              {fmtBytes(b.size)} · загружен {fmtAgo(b.created_at)}
              {b.sync_error && <span style={{ color: 'var(--red-text)' }}> · {b.sync_error}</span>}
            </div>
          </div>
          <span className="mono muted hide-m">{b.sha256.slice(0, 10)}</span>
        </div>
      ))}
    </div>
  )
}

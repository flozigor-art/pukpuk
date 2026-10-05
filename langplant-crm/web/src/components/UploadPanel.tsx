import { ChevronDown, ChevronUp, CircleCheck, RotateCcw, TriangleAlert, X } from 'lucide-react'
import { useState } from 'react'
import { fmtBytes } from '../lib/format'
import { cancel, clearFinished, retry, useUploads } from '../lib/uploads'
import { IconButton, Progress } from './ui'

export function UploadPanel() {
  const items = useUploads().filter((i) => i.status !== 'canceled')
  const [collapsed, setCollapsed] = useState(false)
  if (!items.length) return null
  const active = items.filter((i) => i.status === 'uploading' || i.status === 'queued')
  const total = items.reduce((s, i) => s + i.file.size, 0)
  const sent = items.reduce((s, i) => s + (i.status === 'done' ? i.file.size : i.sent), 0)
  const errors = items.filter((i) => i.status === 'error').length
  return (
    <div className="uploads" role="status">
      <div className="uploads-head">
        <span className="grow">
          {active.length ? `Загрузка: ${active.length} из ${items.length}` : errors ? `Ошибок: ${errors}` : 'Загрузки завершены'}
          <div className="tiny muted nums" style={{ fontWeight: 500 }}>
            {fmtBytes(sent)} из {fmtBytes(total)}
            {active.length > 0 && ' · не закрывайте вкладку'}
          </div>
        </span>
        <IconButton label={collapsed ? 'Развернуть' : 'Свернуть'} size="sm" onClick={() => setCollapsed(!collapsed)}>
          {collapsed ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
        </IconButton>
        {!active.length && (
          <IconButton label="Закрыть" size="sm" onClick={clearFinished}>
            <X size={16} />
          </IconButton>
        )}
      </div>
      {collapsed ? (
        <div style={{ padding: '0 14px 12px' }}>
          <Progress value={total ? sent / total : 0} />
        </div>
      ) : (
        <div className="uploads-list">
          {items.map((it) => (
            <div key={it.key} className="up">
              <div className="row">
                <div className="grow">
                  <div className="name ellipsis">{it.file.name}</div>
                  <div className="tiny muted ellipsis">{it.label}</div>
                </div>
                {it.status === 'done' && <CircleCheck size={17} color="var(--accent)" />}
                {it.status === 'error' && (
                  <IconButton label="Повторить" size="sm" onClick={() => retry(it.key)}>
                    <RotateCcw size={15} />
                  </IconButton>
                )}
                {(it.status === 'uploading' || it.status === 'queued' || it.status === 'error') && (
                  <IconButton label="Отменить" size="sm" onClick={() => cancel(it.key)}>
                    <X size={15} />
                  </IconButton>
                )}
              </div>
              {it.status !== 'done' && <Progress value={it.file.size ? it.sent / it.file.size : 0} tone={it.status === 'error' ? 'red' : undefined} />}
              <div className="tiny muted nums row">
                {it.status === 'queued' && 'В очереди'}
                {it.status === 'uploading' && (
                  <>
                    {fmtBytes(it.sent)} / {fmtBytes(it.file.size)}
                    {it.speed > 0 && ` · ${fmtBytes(it.speed)}/с`}
                  </>
                )}
                {it.status === 'done' && (it.result?.duplicate ? 'Такой файл уже был — привязан без повторной загрузки' : `Загружено · ${fmtBytes(it.file.size)}`)}
                {it.error && (
                  <span style={{ color: it.status === 'error' ? 'var(--red-text)' : 'var(--amber-text)' }} className="row">
                    <TriangleAlert size={12} /> {it.error}
                  </span>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

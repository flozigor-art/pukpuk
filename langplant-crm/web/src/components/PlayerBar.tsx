import { Music, Pause, Play, SkipBack, SkipForward, X } from 'lucide-react'
import { derivedUrl } from '../lib/api'
import { fmtDuration } from '../lib/format'
import { next, prev, seek, stop, toggle, usePlayer } from '../lib/player'
import { IconButton, Spinner, Waveform } from './ui'

export function PlayerBar() {
  const p = usePlayer()
  const t = p.track
  if (!t) return null
  const progress = p.duration ? p.time / p.duration : 0
  return (
    <div className="playerbar">
      <div className="row" style={{ minWidth: 0, gap: 10 }}>
        <div className="tcover" style={{ width: 40, height: 40 }}>
          {t.has_cover ? <img src={derivedUrl(t.sha256, 'thumb.jpg')} alt="" /> : <Music size={16} />}
        </div>
        <div style={{ minWidth: 0 }}>
          <div className="ellipsis" style={{ fontWeight: 600 }}>
            {t.title}
          </div>
          <div className="ellipsis tiny muted">{p.error ?? (t.artist || '—')}</div>
        </div>
      </div>
      <div className="mprogress hide-d">
        <div style={{ width: `${progress * 100}%` }} />
      </div>
      <div className="middle row hide-m" style={{ gap: 10 }}>
        <span className="time">{fmtDuration(p.time)}</span>
        <div className="grow">
          <Waveform peaks={t.peaks} progress={progress} onSeek={seek} bars={140} height={30} />
        </div>
        <span className="time">{fmtDuration(p.duration || t.duration_ms)}</span>
      </div>
      <div className="ctrl">
        <IconButton label="Предыдущий" onClick={prev} className="hide-m">
          <SkipBack size={17} />
        </IconButton>
        <button className="bigplay" onClick={toggle} aria-label={p.playing ? 'Пауза' : 'Играть'}>
          {p.loading ? <Spinner size={16} /> : p.playing ? <Pause size={17} /> : <Play size={17} style={{ marginLeft: 2 }} />}
        </button>
        <IconButton label="Следующий" onClick={next}>
          <SkipForward size={17} />
        </IconButton>
        <IconButton label="Закрыть плеер" onClick={stop}>
          <X size={17} />
        </IconButton>
      </div>
    </div>
  )
}

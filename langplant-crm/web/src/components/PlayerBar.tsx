import { Music, Pause, Play, SkipBack, SkipForward, X } from 'lucide-react'
import { useState } from 'react'
import { derivedUrl } from '../lib/api'
import { fmtDuration } from '../lib/format'
import { next, prev, scrub, seek, stop, toggle, usePlayer } from '../lib/player'
import { useTrack } from '../lib/queries'
import type { Track } from '../lib/types'
import { IconButton, Modal, Spinner, Waveform } from './ui'

/** Waveform of the current track at full resolution (lists carry a downsampled one). */
function usePeaks(t: Track) {
  const full = useTrack(t.id)
  return full.data?.track.peaks ?? t.peaks
}

function Cover({ t, size }: { t: Track; size: number }) {
  return (
    <div className="tcover" style={{ width: size, height: size, borderRadius: size > 80 ? 16 : 8 }}>
      {t.has_cover ? <img src={derivedUrl(t.sha256, 'thumb.jpg')} alt="" /> : <Music size={size > 80 ? 40 : 16} />}
    </div>
  )
}

/** Thin progress line of the phone mini player; press and drag along it to seek. */
function SeekLine({ progress }: { progress: number }) {
  const [drag, setDrag] = useState<number | null>(null)
  const frac = (e: React.PointerEvent) => {
    const r = e.currentTarget.getBoundingClientRect()
    return Math.max(0, Math.min(1, (e.clientX - r.left) / r.width))
  }
  const shown = drag ?? progress
  return (
    <div
      className={drag != null ? 'mscrub dragging hide-d' : 'mscrub hide-d'}
      onPointerDown={(e) => {
        e.currentTarget.setPointerCapture(e.pointerId)
        setDrag(frac(e))
        scrub(frac(e))
      }}
      onPointerMove={(e) => {
        if (drag == null) return
        setDrag(frac(e))
        scrub(frac(e))
      }}
      onPointerUp={(e) => {
        if (drag == null) return
        seek(frac(e))
        setDrag(null)
      }}
      onPointerCancel={() => setDrag(null)}
    >
      <div className="line">
        <div style={{ width: `${shown * 100}%` }} />
      </div>
      <span className="knob" style={{ left: `${shown * 100}%` }} />
    </div>
  )
}

export function PlayerBar() {
  const p = usePlayer()
  const t = p.track
  const [open, setOpen] = useState(false)
  if (!t) return null
  return <Bar t={t} open={open} setOpen={setOpen} p={p} />
}

function Bar({ t, open, setOpen, p }: { t: Track; open: boolean; setOpen: (v: boolean) => void; p: ReturnType<typeof usePlayer> }) {
  const peaks = usePeaks(t)
  const duration = p.duration || t.duration_ms || 0
  const progress = duration ? p.time / duration : 0
  const playBtn = (big?: boolean) => (
    <button className={big ? 'bigplay xl' : 'bigplay'} onClick={toggle} aria-label={p.playing ? 'Пауза' : 'Играть'}>
      {p.loading ? <Spinner size={big ? 22 : 16} /> : p.playing ? <Pause size={big ? 26 : 17} /> : <Play size={big ? 26 : 17} style={{ marginLeft: 2 }} />}
    </button>
  )
  return (
    <div className="playerbar">
      <button className="pb-info" onClick={() => setOpen(true)} aria-label="Открыть плеер">
        <Cover t={t} size={40} />
        <span style={{ minWidth: 0, textAlign: 'left' }}>
          <span className="ellipsis" style={{ display: 'block', fontWeight: 600 }}>
            {t.title}
          </span>
          <span className="ellipsis tiny muted" style={{ display: 'block' }}>
            {p.error ?? (t.artist || 'Неизвестный исполнитель')}
          </span>
        </span>
      </button>
      <div className="middle row hide-m" style={{ gap: 10 }}>
        <span className="time">{fmtDuration(p.time)}</span>
        <Waveform className="grow" peaks={peaks} progress={progress} onSeek={seek} onScrub={scrub} durationMs={duration} height={34} />
        <span className="time">{fmtDuration(duration)}</span>
      </div>
      <div className="ctrl">
        <IconButton label="Предыдущий" onClick={prev} className="hide-m">
          <SkipBack size={17} />
        </IconButton>
        {playBtn()}
        <IconButton label="Следующий" onClick={next}>
          <SkipForward size={17} />
        </IconButton>
        <IconButton label="Закрыть плеер" onClick={stop}>
          <X size={17} />
        </IconButton>
      </div>
      {/* phone: the thin line at the bottom of the mini player is a scrubber too */}
      <SeekLine progress={progress} />

      <Modal open={open} onClose={() => setOpen(false)} title="Сейчас играет">
        <div className="np">
          <Cover t={t} size={176} />
          <div className="np-title">
            <div className="ellipsis" style={{ fontWeight: 650, fontSize: 17 }}>
              {t.title}
            </div>
            <div className="ellipsis muted">{t.artist || 'Неизвестный исполнитель'}</div>
          </div>
          <Waveform className="np-wave" peaks={peaks} progress={progress} onSeek={seek} onScrub={scrub} durationMs={duration} height={64} barWidth={2} />
          <div className="row nums small muted" style={{ justifyContent: 'space-between', marginTop: -6 }}>
            <span>{fmtDuration(p.time)}</span>
            <span>{fmtDuration(duration)}</span>
          </div>
          <div className="row" style={{ justifyContent: 'center', gap: 22 }}>
            <IconButton label="Предыдущий" onClick={prev} className="lg">
              <SkipBack size={22} />
            </IconButton>
            {playBtn(true)}
            <IconButton label="Следующий" onClick={next} className="lg">
              <SkipForward size={22} />
            </IconButton>
          </div>
        </div>
      </Modal>
    </div>
  )
}

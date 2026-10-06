import clsx from 'clsx'
import { Archive, CalendarCheck, CalendarDays, CircleCheck, Clock, Flame, HardDrive, Hourglass, Layers, Plus } from 'lucide-react'
import { Link, useNavigate } from 'react-router'
import { useNewVideo } from '../components/Layout'
import { Avatar, Loading, Progress, StagePill, Thumb } from '../components/ui'
import { DAY_STATUS } from '../lib/labels'
import { diffDays, fmtAgo, fmtBytes, fmtDay, fmtLeft, plural, relDay, weekday, WEEKDAYS } from '../lib/format'
import { useDashboard, useDicts } from '../lib/queries'
import type { Activity, Day, PlanStats } from '../lib/types'
import { ActivityText } from './VideoPage'

export default function Dashboard() {
  const { data, isLoading } = useDashboard()
  const d = useDicts()
  if (isLoading || !data) return <Loading />
  const plan = data.plan
  const todayDay = plan.days.find((x) => x.date === plan.today)
  return (
    <div className="page">
      <div className="page-head hide-m">
        <div>
          <h1>Привет, {d.me.name}</h1>
          <div className="sub">{fmtDay(plan.today, { weekday: true, long: true })}</div>
        </div>
      </div>
      <div className="dash-grid">
        <TodayCard plan={plan} day={todayDay} />
        <Stat
          className="span-3"
          icon={<Flame size={15} />}
          label="Серия"
          value={plan.streak}
          unit={plural(plan.streak, 'день', 'дня', 'дней')}
          foot="дней подряд с новым роликом"
        />
        <Stat
          className="span-3"
          icon={<Layers size={15} />}
          label="Резерв"
          value={plan.reserve}
          unit={plural(plan.reserve, 'ролик', 'ролика', 'роликов')}
          foot="готовы, но без даты (п.6.9)"
        />
        <Stat
          className="span-3"
          icon={<CalendarCheck size={15} />}
          label="План покрыт"
          value={plan.covered_until ? diffDays(plan.covered_until, plan.today) + 1 : 0}
          unit={plural(plan.covered_until ? diffDays(plan.covered_until, plan.today) + 1 : 0, 'день', 'дня', 'дней')}
          foot={plan.covered_until ? `до ${fmtDay(plan.covered_until)} включительно` : 'на сегодня ничего не запланировано'}
        />
        <Stat
          className="span-3"
          icon={<Hourglass size={15} />}
          label="Пропуски"
          value={plan.missed_window}
          unit={`за ${plan.window} дн`}
          tone={plan.missed_window >= 6 ? 'red' : plan.missed_window >= 4 ? 'amber' : undefined}
          foot={plan.missed_row > 0 ? `${plan.missed_row} ${plural(plan.missed_row, 'день', 'дня', 'дней')} подряд без публикации` : 'вчера всё по плану'}
        />

        <div className="span-7 dash-col">
          <div className="card dash-archive">
            <div className="card-head">
              <Archive size={16} />
              <h3>
                Архив материалов
                <span className="hint">финал, чистый голос и дорожка без голоса — в течение 48 ч после публикации</span>
              </h3>
              {data.archive.length > 0 && (
                <Link to="/videos?pub=archive" className="more btn ghost sm">
                  Все
                </Link>
              )}
            </div>
            <div className="card-body">
              {data.archive.length === 0 ? (
                <div className="row muted" style={{ padding: '10px 0' }}>
                  <CircleCheck size={17} color="var(--accent)" /> По всем опубликованным роликам архив собран
                </div>
              ) : (
                <div className="list">
                  {data.archive.map((v) => {
                    const left = v.archive.deadline_at ? fmtLeft(v.archive.deadline_at) : null
                    const missingKinds = [...new Set(v.variants.flatMap((x) => (x.first_published_at ? x.missing : [])))]
                    return (
                      <Link to={`/videos/${v.id}`} key={v.id} className="list-row">
                        <Thumb sha={v.thumb} size="sm" />
                        <div className="grow">
                          <div className="title clamp-m">
                            <span className="code">{v.code}</span> {v.title}
                          </div>
                          <div className="small muted row-sub">
                            {left && <span className={clsx('chip sm hide-d', left.overdue ? 'red' : left.urgent ? 'amber' : '')}>{left.text}</span>}
                            <span className="ellipsis">нет: {missingKinds.map((k) => d.kindByKey.get(k)?.name ?? k).join(', ').toLowerCase()}</span>
                          </div>
                        </div>
                        {left && <span className={clsx('chip sm hide-m', left.overdue ? 'red' : left.urgent ? 'amber' : '')}>{left.text}</span>}
                      </Link>
                    )
                  })}
                </div>
              )}
            </div>
          </div>
          <div className="card dash-work">
            <div className="card-head">
              <Layers size={16} />
              <h3>В работе</h3>
              <Link to="/videos?view=board" className="more btn ghost sm">
                Доска
              </Link>
            </div>
            <div className="card-body">
              <div className="chips" style={{ marginBottom: 10 }}>
                {d.activeStages.map((s) => (
                  <Link key={s.id} to={`/videos?stage=${s.id}`} className="chip">
                    <span className="dot" style={{ background: s.color }} />
                    {s.name}
                    <b style={{ marginLeft: 2 }}>{data.stages[String(s.id)] ?? 0}</b>
                  </Link>
                ))}
              </div>
              {data.in_work.length === 0 ? (
                <div className="muted small">Нет роликов в работе</div>
              ) : (
                <div className="list">
                  {data.in_work.map((v) => (
                    <Link to={`/videos/${v.id}`} key={v.id} className="list-row">
                      <Thumb sha={v.thumb} size="sm" />
                      <div className="grow">
                        <div className="title clamp-m">
                          <span className="code">{v.code}</span> {v.title}
                        </div>
                        <div className="small muted">
                          {v.plan_date ? `план: ${relDay(v.plan_date)}` : 'без даты'}
                          {v.check_total > 0 && ` · чек-лист ${v.check_done}/${v.check_total}`}
                        </div>
                      </div>
                      <StagePill stage={v.stage_id ? d.stageById.get(v.stage_id) : null} size="sm" />
                      {v.assignee_id && <Avatar user={d.userById.get(v.assignee_id)} size="sm" />}
                    </Link>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>
        <div className="span-5 dash-col">
          <div className="card dash-feed">
            <div className="card-head">
              <Clock size={16} />
              <h3>Активность</h3>
            </div>
            <div className="card-body feed-scroll">
              {data.activity.length === 0 ? <div className="muted small">Пока пусто</div> : data.activity.slice(0, 12).map((a) => <FeedItem key={a.id} a={a} />)}
            </div>
          </div>
          <div className="card dash-storage">
            <div className="card-head">
              <HardDrive size={16} />
              <h3>Хранилище</h3>
              <Link to="/storage" className="more btn ghost sm">
                Подробнее
              </Link>
            </div>
            <div className="card-body form" style={{ gap: 12 }}>
              <div className="row">
                <span className={clsx('dot', data.storage.online ? 'on' : 'off')} />
                <b>{data.storage.online ? 'Комп на связи' : 'Комп офлайн'}</b>
                {!data.storage.online && data.storage.last_seen > 0 && <span className="muted small">был {fmtAgo(data.storage.last_seen)}</span>}
              </div>
              <div>
                <div className="row small" style={{ marginBottom: 6 }}>
                  <span className="grow text-2">Буфер на сервере</span>
                  <span className="nums muted">
                    {fmtBytes(data.storage.buffer_used)} / {fmtBytes(data.storage.buffer_max)}
                  </span>
                </div>
                <Progress value={data.storage.buffer_used / data.storage.buffer_max} tone={data.storage.buffer_used / data.storage.buffer_max > 0.8 ? 'red' : 'blue'} />
              </div>
              <div className="small text-2">
                {data.storage.pending_count > 0
                  ? `Ждут переноса на комп: ${data.storage.pending_count} ${plural(data.storage.pending_count, 'файл', 'файла', 'файлов')} (${fmtBytes(data.storage.pending_bytes)})`
                  : 'Все файлы перенесены на комп'}
                {data.storage.missing_count > 0 && <div style={{ color: 'var(--red-text)' }}>Потеряно файлов: {data.storage.missing_count}</div>}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function TodayCard({ plan, day }: { plan: PlanStats; day?: Day }) {
  const nav = useNavigate()
  const newVideo = useNewVideo()
  const st = plan.today_status
  const first = day?.items.filter((i) => i.kind === 'first' || i.kind === 'planned') ?? []
  const tone = st === 'done' ? 'var(--accent)' : st === 'planned' ? 'var(--blue)' : 'var(--amber)'
  const title = st === 'done' ? 'Сегодняшний ролик вышел' : st === 'planned' ? 'Ролик на сегодня запланирован' : st === 'off' ? 'План ещё не начался' : 'На сегодня ещё нет ролика'
  return (
    <div className="card span-12">
      <div className="today">
        <div className="ring" style={{ background: `color-mix(in srgb, ${tone} 15%, transparent)`, color: tone }}>
          {st === 'done' ? <CircleCheck size={30} /> : st === 'planned' ? <CalendarCheck size={28} /> : <CalendarDays size={28} />}
        </div>
        <div className="grow">
          <h2>{title}</h2>
          <p>
            {first.length > 0
              ? first.map((i, n) => (
                  <span key={i.video_id}>
                    {n > 0 && ', '}
                    <Link to={`/videos/${i.video_id}`} style={{ textDecoration: 'underline', textUnderlineOffset: 3 }}>
                      {i.code} «{i.title}»
                    </Link>
                  </span>
                ))
              : `План: ${plan.quota} новый уникальный ролик в день (TikTok + Shorts + Reels считаются одним)`}
          </p>
        </div>
        <div className="row hide-m">
          {st !== 'done' && st !== 'planned' && (
            <>
              <button className="btn" onClick={() => nav('/calendar')}>
                <CalendarDays size={15} /> Календарь
              </button>
              <button className="btn primary" onClick={() => newVideo(plan.today)}>
                <Plus size={15} /> Ролик на сегодня
              </button>
            </>
          )}
        </div>
      </div>
      <div className="today-strip">
        <DayStrip days={plan.days} today={plan.today} onClick={() => nav('/calendar')} />
        <div className="legend" style={{ marginTop: 10 }}>
          <span>
            <i className="st-done" /> вышел
          </span>
          <span>
            <i className="st-planned" /> запланирован
          </span>
          <span>
            <i className="st-missed" /> пропуск
          </span>
          <span>
            <i className="st-excused" /> уважительная причина
          </span>
          <span>
            <i className="st-empty" /> пусто
          </span>
        </div>
      </div>
    </div>
  )
}

export function DayStrip({ days, today, onClick }: { days: Day[]; today: string; onClick?: (d: Day) => void }) {
  return (
    <div className="strip">
      {days.map((x) => {
        const dist = Math.abs(diffDays(x.date, today))
        return (
          <div
            key={x.date}
            className={clsx('d', `st-${x.status}`, x.date === today && 'is-today', dist > 6 && 'far')}
            title={`${fmtDay(x.date, { weekday: true })} — ${DAY_STATUS[x.status]}`}
            onClick={() => onClick?.(x)}
          >
            <span style={{ opacity: 0.7 }}>{WEEKDAYS[weekday(x.date)].toLowerCase()}</span>
            {Number(x.date.slice(8))}
          </div>
        )
      })}
    </div>
  )
}

function Stat({ icon, label, value, unit, foot, tone, className }: { icon: React.ReactNode; label: string; value: number; unit?: string; foot?: string; tone?: 'amber' | 'red'; className?: string }) {
  return (
    <div className={clsx('card stat', className)}>
      <div className="label">
        {icon}
        {label}
      </div>
      <div className="value" style={tone ? { color: `var(--${tone}-text)` } : undefined}>
        {value}
        {unit && <small>{unit}</small>}
      </div>
      {foot && <div className="foot">{foot}</div>}
    </div>
  )
}

export function FeedItem({ a, showVideo = true }: { a: Activity; showVideo?: boolean }) {
  const d = useDicts()
  const u = a.user_id ? d.userById.get(a.user_id) : undefined
  return (
    <div className="feed-item">
      <Avatar user={u} size="sm" />
      <div className="grow" style={{ minWidth: 0 }}>
        <div className="what">
          <b>{u?.name ?? 'Система'}</b> <ActivityText a={a} />
        </div>
        <div className="when">
          {fmtAgo(a.created_at)}
          {showVideo && a.video_id && a.video_num != null && (
            <>
              {' · '}
              <Link to={`/videos/${a.video_id}`}>
                {d.settings.code_prefix}-{String(a.video_num).padStart(4, '0')} {a.video_title}
              </Link>
            </>
          )}
        </div>
      </div>
    </div>
  )
}


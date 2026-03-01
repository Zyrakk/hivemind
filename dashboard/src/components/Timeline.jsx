import { useMemo, useState } from 'react';

const eventStyles = {
  worker_started: { icon: '>>', color: 'text-hivemind-green', bg: 'bg-hivemind-green/15 border-hivemind-green/30' },
  worker_failed: { icon: 'x', color: 'text-hivemind-red', bg: 'bg-hivemind-red/15 border-hivemind-red/30' },
  task_completed: { icon: 'v', color: 'text-hivemind-green', bg: 'bg-hivemind-green/15 border-hivemind-green/30' },
  pr_created: { icon: 'PR', color: 'text-hivemind-blue', bg: 'bg-hivemind-blue/15 border-hivemind-blue/30' },
  input_needed: { icon: '!', color: 'text-hivemind-yellow', bg: 'bg-hivemind-yellow/15 border-hivemind-yellow/30' }
};

const PAGE_SIZE = 20;

function formatTimestamp(value) {
  if (!value) {
    return '--';
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '--';
  }

  return new Intl.DateTimeFormat('es-ES', {
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  }).format(date);
}

export default function Timeline({ events }) {
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE);

  const sortedEvents = useMemo(
    () =>
      [...events].sort(
        (a, b) => new Date(b.timestamp ?? 0).getTime() - new Date(a.timestamp ?? 0).getTime()
      ),
    [events]
  );

  const visibleEvents = sortedEvents.slice(0, visibleCount);
  const hasMore = sortedEvents.length > visibleCount;

  return (
    <section className="rounded-xl border border-slate-700 bg-hivemind-card p-4 shadow-panel">
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-bold text-hivemind-text">Timeline Reciente</h2>
        <span className="text-xs text-hivemind-muted">{sortedEvents.length} eventos</span>
      </div>

      {visibleEvents.length === 0 ? (
        <p className="rounded-lg border border-dashed border-slate-600 p-4 text-sm text-hivemind-muted">
          No hay eventos recientes
        </p>
      ) : (
        <div className="space-y-3">
          {visibleEvents.map((event) => {
            const style = eventStyles[event.event_type] ?? {
              icon: '?',
              color: 'text-hivemind-gray',
              bg: 'bg-slate-600/20 border-slate-500/40'
            };

            return (
              <article key={event.id} className="flex items-start gap-3 rounded-lg border border-slate-700 p-3">
                <span
                  className={`inline-flex h-7 min-w-7 items-center justify-center rounded-full border px-1 text-xs font-bold ${style.bg} ${style.color}`}
                >
                  {style.icon}
                </span>
                <div className="min-w-0">
                  <p className="text-xs text-hivemind-muted">{formatTimestamp(event.timestamp)}</p>
                  <p className="text-sm text-hivemind-text">{event.description}</p>
                </div>
              </article>
            );
          })}
        </div>
      )}

      {hasMore ? (
        <div className="mt-4">
          <button
            type="button"
            onClick={() => setVisibleCount((current) => current + PAGE_SIZE)}
            className="rounded-md border border-slate-600 px-3 py-1.5 text-sm text-hivemind-muted transition hover:border-slate-500 hover:text-hivemind-text"
          >
            Ver mas
          </button>
        </div>
      ) : null}
    </section>
  );
}

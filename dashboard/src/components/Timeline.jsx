import { useMemo, useState } from 'react';

const eventStyles = {
  worker_started: { color: 'bg-hivemind-blue' },
  worker_failed: { color: 'bg-hivemind-red' },
  task_completed: { color: 'bg-hivemind-green' },
  pr_created: { color: 'bg-hivemind-green' },
  input_needed: { color: 'bg-hivemind-yellow' },
  default: { color: 'bg-hivemind-dim' }
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

  return new Intl.DateTimeFormat('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
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
    <section className="bg-hivemind-surface px-3 py-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-[8px] uppercase tracking-[0.15em] text-hivemind-dim">ACTIVITY LOG</span>
        <span className="text-[8px] uppercase tracking-[0.1em] text-hivemind-dim">{sortedEvents.length} events</span>
      </div>

      {visibleEvents.length === 0 ? (
        <p className="mt-2 border border-dashed border-hivemind-border px-3 py-4 text-[9px] text-hivemind-dim">
          No recent events
        </p>
      ) : (
        <div className="mt-2 space-y-1">
          {visibleEvents.map((event) => {
            const style = eventStyles[event.event_type] ?? eventStyles.default;

            return (
              <article key={event.id} className="flex items-start gap-2">
                <span className={`mt-[4px] h-1 w-1 shrink-0 ${style.color}`} />
                <span className="shrink-0 text-[8px] text-hivemind-dim">{formatTimestamp(event.timestamp)}</span>
                <p className="min-w-0 text-[9px] text-hivemind-muted">{event.description}</p>
              </article>
            );
          })}
        </div>
      )}

      {hasMore ? (
        <div className="mt-2">
          <button
            type="button"
            onClick={() => setVisibleCount((current) => current + PAGE_SIZE)}
            className="cursor-pointer border border-hivemind-border px-2 py-[3px] text-[8px] text-hivemind-muted transition-colors duration-150 hover:text-hivemind-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-hivemind-blue focus-visible:outline-offset-0"
          >
            show more
          </button>
        </div>
      ) : null}
    </section>
  );
}

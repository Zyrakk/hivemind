import SectionHeader from './SectionHeader';

function formatRelativeTime(dateValue) {
  if (!dateValue) {
    return '--';
  }

  const date = new Date(dateValue);
  if (Number.isNaN(date.getTime())) {
    return '--';
  }

  const diffMs = Date.now() - date.getTime();
  const diffMin = Math.max(0, Math.floor(diffMs / 60000));

  if (diffMin < 1) {
    return '<1m';
  }
  if (diffMin < 60) {
    return `${diffMin}m`;
  }

  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) {
    return `${diffHours}h`;
  }

  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}d`;
}

const statusDot = {
  running: 'bg-hivemind-green',
  paused: 'bg-hivemind-gray',
  completed: 'bg-hivemind-blue',
  blocked: 'bg-hivemind-red',
  failed: 'bg-hivemind-red'
};

export default function WorkerList({ workers }) {
  const sortedWorkers = [...workers].sort((a, b) => {
    const aTs = new Date(a.started_at ?? 0).getTime();
    const bTs = new Date(b.started_at ?? 0).getTime();
    return bTs - aTs;
  });

  return (
    <section className="flex h-full min-h-0 flex-col bg-hivemind-surface px-[18px] py-[14px]">
      <SectionHeader label="WORKERS" count={sortedWorkers.length} color="hivemind-blue" />

      {sortedWorkers.length === 0 ? (
        <p className="mt-3 border border-dashed border-hivemind-border px-4 py-5 text-[11px] text-hivemind-dim">
          No active workers
        </p>
      ) : (
        <div className="mt-2">
          {sortedWorkers.map((worker) => {
            const dotClass = statusDot[worker.status] ?? 'bg-hivemind-muted';

            return (
              <article
                key={worker.id}
                className="mb-[14px] border-b border-hivemind-border pb-3 last:mb-0 last:border-b-0 last:pb-0"
              >
                <div className="flex items-center justify-between gap-2">
                  <p className="min-w-0 truncate text-[12px] font-semibold text-hivemind-text">
                    {worker.project_name ?? worker.project_id}
                  </p>
                  <p className="shrink-0 text-[11px] text-hivemind-dim">{formatRelativeTime(worker.started_at)}</p>
                </div>

                <p
                  className="mt-2 text-[12px] text-hivemind-muted"
                  style={{
                    overflow: 'hidden',
                    display: '-webkit-box',
                    WebkitLineClamp: 2,
                    WebkitBoxOrient: 'vertical'
                  }}
                  title={worker.task_description}
                >
                  {worker.task_description}
                </p>

                <div className="mt-2 flex items-center justify-between gap-2">
                  <p className="min-w-0 truncate text-[11px] text-hivemind-dim" title={worker.branch}>
                    {worker.branch}
                  </p>
                  <span className={`h-[5px] w-[5px] shrink-0 ${dotClass}`} aria-hidden="true" />
                </div>
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}

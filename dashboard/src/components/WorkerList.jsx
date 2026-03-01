function formatRelativeTime(dateValue) {
  if (!dateValue) {
    return '-';
  }

  const date = new Date(dateValue);
  if (Number.isNaN(date.getTime())) {
    return '-';
  }

  const diffMs = Date.now() - date.getTime();
  const diffMin = Math.max(0, Math.floor(diffMs / 60000));

  if (diffMin < 1) {
    return '<1 min';
  }
  if (diffMin < 60) {
    return `${diffMin} min`;
  }

  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) {
    return `${diffHours} h`;
  }

  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays} d`;
}

const statusBadge = {
  running: 'bg-hivemind-green/15 text-hivemind-green border-hivemind-green/30',
  paused: 'bg-hivemind-gray/15 text-hivemind-gray border-hivemind-gray/30',
  completed: 'bg-hivemind-blue/15 text-hivemind-blue border-hivemind-blue/30',
  blocked: 'bg-hivemind-red/15 text-hivemind-red border-hivemind-red/30',
  failed: 'bg-hivemind-red/15 text-hivemind-red border-hivemind-red/30'
};

export default function WorkerList({ workers }) {
  const sortedWorkers = [...workers].sort((a, b) => {
    const aTs = new Date(a.started_at ?? 0).getTime();
    const bTs = new Date(b.started_at ?? 0).getTime();
    return bTs - aTs;
  });

  return (
    <section className="rounded-xl border border-slate-700 bg-hivemind-card p-4 shadow-panel">
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-bold text-hivemind-text">Workers Activos Globales</h2>
        <span className="text-sm text-hivemind-muted">{sortedWorkers.length}</span>
      </div>

      {sortedWorkers.length === 0 ? (
        <p className="rounded-lg border border-dashed border-slate-600 p-4 text-sm text-hivemind-muted">
          No hay workers activos
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="min-w-full text-left text-sm">
            <thead className="text-xs uppercase tracking-wide text-hivemind-muted">
              <tr className="border-b border-slate-700">
                <th className="px-3 py-2">Proyecto</th>
                <th className="px-3 py-2">Tarea</th>
                <th className="px-3 py-2">Rama</th>
                <th className="px-3 py-2">Tiempo activo</th>
                <th className="px-3 py-2">Estado</th>
              </tr>
            </thead>
            <tbody>
              {sortedWorkers.map((worker) => (
                <tr
                  key={worker.id}
                  className="border-b border-slate-800 last:border-b-0 hover:bg-slate-700/20"
                >
                  <td className="px-3 py-3 font-medium text-hivemind-text">
                    {worker.project_name ?? worker.project_id}
                  </td>
                  <td className="max-w-[280px] truncate px-3 py-3 text-hivemind-muted" title={worker.task_description}>
                    {worker.task_description}
                  </td>
                  <td className="px-3 py-3 font-mono text-xs text-hivemind-muted">{worker.branch}</td>
                  <td className="px-3 py-3 text-hivemind-muted">{formatRelativeTime(worker.started_at)}</td>
                  <td className="px-3 py-3">
                    <span
                      className={`rounded-full border px-2 py-1 text-xs font-semibold ${statusBadge[worker.status] ?? statusBadge.paused}`}
                    >
                      {worker.status}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

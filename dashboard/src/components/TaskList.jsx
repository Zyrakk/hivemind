import { useMemo, useState } from 'react';

const filters = [
  { key: 'all', label: 'All' },
  { key: 'pending', label: 'Pending' },
  { key: 'in_progress', label: 'In Progress' },
  { key: 'completed', label: 'Completed' },
  { key: 'blocked', label: 'Blocked' }
];

const statusStyles = {
  pending: 'bg-slate-600/20 text-slate-300 border-slate-500/40',
  in_progress: 'bg-hivemind-blue/15 text-hivemind-blue border-hivemind-blue/30',
  completed: 'bg-hivemind-green/15 text-hivemind-green border-hivemind-green/30',
  blocked: 'bg-hivemind-red/15 text-hivemind-red border-hivemind-red/30',
  failed: 'bg-hivemind-red/15 text-hivemind-red border-hivemind-red/30'
};

function normalizeDependsOn(dependsOn) {
  if (Array.isArray(dependsOn)) {
    return dependsOn;
  }
  return [];
}

function shouldIncludeTask(task, filterKey) {
  if (filterKey === 'all') {
    return true;
  }
  if (filterKey === 'blocked') {
    return task.status === 'blocked' || task.status === 'failed';
  }
  return task.status === filterKey;
}

export default function TaskList({ tasks, workers }) {
  const [activeFilter, setActiveFilter] = useState('all');
  const [expandedTaskID, setExpandedTaskID] = useState(null);

  const workerByID = useMemo(() => {
    const map = new Map();
    workers.forEach((worker) => {
      map.set(worker.id, worker);
    });
    return map;
  }, [workers]);

  const filteredTasks = useMemo(
    () => tasks.filter((task) => shouldIncludeTask(task, activeFilter)),
    [tasks, activeFilter]
  );

  const toggleExpanded = (taskID) => {
    setExpandedTaskID((current) => (current === taskID ? null : taskID));
  };

  return (
    <section className="rounded-xl border border-slate-700 bg-hivemind-card p-4 shadow-panel">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-bold text-hivemind-text">Tareas</h2>
        <div className="flex flex-wrap items-center gap-2">
          {filters.map((filter) => (
            <button
              key={filter.key}
              type="button"
              onClick={() => setActiveFilter(filter.key)}
              className={`rounded-md border px-3 py-1 text-xs font-medium transition ${
                activeFilter === filter.key
                  ? 'border-hivemind-blue/70 bg-hivemind-blue/20 text-hivemind-blue'
                  : 'border-slate-600 text-hivemind-muted hover:border-slate-500 hover:text-hivemind-text'
              }`}
            >
              {filter.label}
            </button>
          ))}
        </div>
      </div>

      {filteredTasks.length === 0 ? (
        <p className="rounded-lg border border-dashed border-slate-600 p-4 text-sm text-hivemind-muted">
          No hay tareas para este filtro
        </p>
      ) : (
        <div className="space-y-3">
          {filteredTasks.map((task) => {
            const dependsOn = normalizeDependsOn(task.depends_on);
            const assignedWorker = task.assigned_worker_id
              ? workerByID.get(task.assigned_worker_id)
              : null;
            const expanded = expandedTaskID === task.id;

            return (
              <article
                key={task.id}
                className={`rounded-lg border p-3 transition ${
                  task.status === 'blocked'
                    ? 'border-hivemind-red/40 bg-hivemind-red/5'
                    : 'border-slate-700 bg-slate-800/40'
                }`}
              >
                <button
                  type="button"
                  onClick={() => toggleExpanded(task.id)}
                  className="w-full text-left"
                >
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div>
                      <h3 className="font-semibold text-hivemind-text">{task.title}</h3>
                      <p className="mt-1 text-xs text-hivemind-muted">
                        Worker:{' '}
                        <span className="text-hivemind-text">
                          {assignedWorker ? assignedWorker.session_id : 'sin asignar'}
                        </span>
                      </p>
                    </div>
                    <span
                      className={`rounded-full border px-2 py-1 text-xs font-semibold ${statusStyles[task.status] ?? statusStyles.pending}`}
                    >
                      {task.status}
                    </span>
                  </div>

                  <div className="mt-2 text-xs text-hivemind-muted">
                    Dependencias:{' '}
                    {dependsOn.length > 0 ? dependsOn.join(', ') : 'sin dependencias'}
                  </div>

                  {task.status === 'blocked' ? (
                    <p className="mt-2 text-xs font-medium text-hivemind-red">
                      Bloqueada por: {dependsOn.length > 0 ? dependsOn.join(', ') : 'dependencia pendiente'}
                    </p>
                  ) : null}
                </button>

                {expanded ? (
                  <div className="mt-3 border-t border-slate-700 pt-3 text-sm text-hivemind-muted">
                    {task.description || 'Sin descripcion'}
                  </div>
                ) : null}
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}

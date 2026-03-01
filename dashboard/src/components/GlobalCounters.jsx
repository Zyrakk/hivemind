const counterItems = [
  { key: 'active_workers', label: 'Workers Activos' },
  { key: 'pending_tasks', label: 'Tareas Pendientes' },
  { key: 'pending_reviews', label: 'PRs por Revisar' }
];

export default function GlobalCounters({ counters }) {
  return (
    <section className="grid grid-cols-1 gap-4 sm:grid-cols-3">
      {counterItems.map((item) => (
        <article
          key={item.key}
          className="rounded-xl border border-slate-700 bg-hivemind-card px-4 py-5 shadow-panel"
        >
          <p className="text-sm text-hivemind-muted">{item.label}</p>
          <p className="mt-2 text-3xl font-extrabold text-hivemind-text">
            {counters?.[item.key] ?? 0}
          </p>
        </article>
      ))}
    </section>
  );
}

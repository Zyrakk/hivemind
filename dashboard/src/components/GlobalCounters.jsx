const counterItems = [
  {
    key: 'active_workers',
    label: 'W',
    valueClass: 'text-hivemind-green',
    dynamicClass: false
  },
  {
    key: 'pending_tasks',
    label: 'Q',
    valueClass: 'text-hivemind-yellow',
    dynamicClass: true
  },
  {
    key: 'pending_reviews',
    label: 'R',
    valueClass: 'text-hivemind-blue',
    dynamicClass: true
  }
];

function toCount(value) {
  const numeric = Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0;
  return String(numeric).padStart(2, '0');
}

export default function GlobalCounters({ counters }) {
  return (
    <section className="grid grid-cols-3 gap-px bg-hivemind-border">
      {counterItems.map((item) => {
        const rawValue = counters?.[item.key] ?? 0;
        const count = Number.isFinite(rawValue) ? Math.max(0, Math.floor(rawValue)) : 0;
        const valueClass = item.dynamicClass && count === 0 ? 'text-hivemind-dim' : item.valueClass;

        return (
          <article key={item.key} className="bg-hivemind-surface px-3 py-2">
            <p className="flex items-end gap-1 leading-none">
              <span className={`text-[22px] font-bold ${valueClass}`}>{toCount(count)}</span>
              <span className="pb-[2px] text-[8px] uppercase tracking-[0.15em] text-hivemind-dim">
                {item.label}
              </span>
            </p>
          </article>
        );
      })}
    </section>
  );
}

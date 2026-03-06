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
    <section className="grid grid-cols-3 gap-[2px] bg-hivemind-border">
      {counterItems.map((item) => {
        const rawValue = counters?.[item.key] ?? 0;
        const count = Number.isFinite(rawValue) ? Math.max(0, Math.floor(rawValue)) : 0;
        const valueClass = item.dynamicClass && count === 0 ? 'text-hivemind-dim' : item.valueClass;

        return (
          <article key={item.key} className="bg-hivemind-surface px-[18px] py-[14px]">
            <p className="leading-none">
              <span className={`text-[32px] font-bold ${valueClass}`}>{toCount(count)}</span>
            </p>
            <p className="mt-1 text-[10px] uppercase tracking-[0.15em] text-hivemind-dim">{item.label}</p>
          </article>
        );
      })}
    </section>
  );
}

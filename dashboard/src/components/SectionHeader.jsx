const colorClassMap = {
  'hivemind-green': 'text-hivemind-green',
  'hivemind-yellow': 'text-hivemind-yellow',
  'hivemind-blue': 'text-hivemind-blue',
  'hivemind-red': 'text-hivemind-red',
  'hivemind-gray': 'text-hivemind-gray',
  'hivemind-muted': 'text-hivemind-muted',
  'hivemind-dim': 'text-hivemind-dim'
};

export default function SectionHeader({ label, count, color = 'hivemind-dim' }) {
  const countClass = colorClassMap[color] ?? 'text-hivemind-dim';

  return (
    <div className="flex items-center justify-between gap-2">
      <span className="text-[8px] uppercase tracking-[0.15em] text-hivemind-dim">{label}</span>
      {typeof count === 'number' ? (
        <span className={`shrink-0 text-[8px] uppercase tracking-[0.15em] ${countClass}`}>{count}</span>
      ) : null}
    </div>
  );
}

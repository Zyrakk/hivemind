import SectionHeader from './SectionHeader';

function normalizeItems(content) {
  if (Array.isArray(content)) {
    return content;
  }

  if (typeof content === 'string') {
    return content
      .split('\n')
      .map((line) => line.replace(/^[-*]\s*/, '').trim())
      .filter(Boolean);
  }

  return [];
}

export default function ContributeNow({ content }) {
  const items = normalizeItems(content);

  return (
    <section className="border-l-2 border-l-hivemind-blue bg-hivemind-surface px-3 py-2.5">
      <SectionHeader label="CONTRIBUTE NOW" color="hivemind-blue" />

      {items.length === 0 ? (
        <p className="mt-2 border border-dashed border-hivemind-border px-3 py-3 text-[9px] text-hivemind-dim">
          No contribution notes available
        </p>
      ) : (
        <ul className="mt-2 space-y-1 text-[10px] text-hivemind-muted">
          {items.map((item) => (
            <li key={item} className="flex items-start gap-1.5">
              <span className="text-hivemind-blue">▸</span>
              <span>{item}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

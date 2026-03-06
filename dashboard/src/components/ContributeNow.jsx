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
    <section className="border-l-2 border-l-hivemind-blue bg-hivemind-surface px-[18px] py-[14px]">
      <SectionHeader label="CONTRIBUTE NOW" color="hivemind-blue" />

      {items.length === 0 ? (
        <p className="mt-3 border border-dashed border-hivemind-border px-4 py-4 text-[11px] text-hivemind-dim">
          No contribution notes available
        </p>
      ) : (
        <ul className="mt-3 space-y-2 text-[12px] text-hivemind-muted">
          {items.map((item) => (
            <li key={item} className="flex items-start gap-2">
              <span className="text-hivemind-blue">▸</span>
              <span>{item}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

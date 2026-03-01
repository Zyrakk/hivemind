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

  if (items.length === 0) {
    return (
      <section className="rounded-xl border border-hivemind-blue/40 bg-hivemind-blue/10 p-4 shadow-panel">
        <h2 className="text-lg font-bold text-hivemind-blue">Para contribuir ahora necesitas saber:</h2>
        <p className="mt-3 text-sm text-hivemind-muted">No hay guidance disponible</p>
      </section>
    );
  }

  return (
    <section className="rounded-xl border border-hivemind-blue/40 bg-hivemind-blue/10 p-4 shadow-panel">
      <h2 className="text-lg font-bold text-hivemind-blue">Para contribuir ahora necesitas saber:</h2>
      <ul className="mt-3 list-disc space-y-1.5 pl-5 text-sm text-hivemind-text">
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    </section>
  );
}

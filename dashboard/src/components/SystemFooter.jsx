export default function SystemFooter({ units, workers }) {
  const hasCounts = typeof units === 'number' && typeof workers === 'number';

  return (
    <footer className="border-t border-hivemind-border bg-[#0d0d0d] px-5 py-2 text-[10px] uppercase tracking-[0.12em] text-hivemind-dim">
      <div className="mx-auto flex w-full max-w-[1280px] items-center justify-between gap-2">
        <span>hivemind orchestrator</span>
        <span>{hasCounts ? `${units} units / ${workers} workers` : 'k3s / sqlite / cloudflare tunnel'}</span>
      </div>
    </footer>
  );
}

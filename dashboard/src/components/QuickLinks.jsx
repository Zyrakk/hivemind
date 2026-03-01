const linkConfig = [
  { key: 'repository', label: 'Repositorio', icon: 'GH' },
  { key: 'open_prs', label: 'PRs abiertos', icon: 'PR' },
  { key: 'agents_md', label: 'AGENTS.md', icon: 'MD' },
  { key: 'active_branch', label: 'Rama activa', icon: 'BR' }
];

function resolveLink(quickLinks, key) {
  if (key === 'active_branch') {
    const branch = quickLinks?.active_branch;
    if (!branch || !branch.url) {
      return null;
    }
    return {
      href: branch.url,
      label: `${linkConfig.find((item) => item.key === key)?.label ?? 'Rama'}: ${branch.name ?? 'active'}`
    };
  }

  const href = quickLinks?.[key];
  if (!href) {
    return null;
  }

  return {
    href,
    label: linkConfig.find((item) => item.key === key)?.label ?? key
  };
}

export default function QuickLinks({ quickLinks }) {
  const entries = linkConfig
    .map((item) => ({ ...item, resolved: resolveLink(quickLinks, item.key) }))
    .filter((item) => item.resolved);

  if (entries.length === 0) {
    return <p className="text-sm text-hivemind-muted">No hay links disponibles</p>;
  }

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {entries.map((item) => (
        <a
          key={item.key}
          href={item.resolved.href}
          target="_blank"
          rel="noreferrer"
          className="flex items-center gap-3 rounded-lg border border-slate-600 bg-slate-800/50 px-3 py-2 text-sm text-hivemind-muted transition hover:border-slate-500 hover:text-hivemind-text"
        >
          <span className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-slate-500 text-xs font-bold text-hivemind-blue">
            {item.icon}
          </span>
          <span className="truncate">{item.resolved.label}</span>
        </a>
      ))}
    </div>
  );
}

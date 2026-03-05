const linkConfig = [
  { key: 'repository', label: 'Repository', icon: 'GH' },
  { key: 'open_prs', label: 'Open PRs', icon: 'PR' },
  { key: 'agents_md', label: 'Agents', icon: 'MD' },
  { key: 'active_branch', label: 'Active branch', icon: 'BR' }
];

function resolveLink(quickLinks, key) {
  if (key === 'active_branch') {
    const branch = quickLinks?.active_branch;
    if (!branch || !branch.url) {
      return null;
    }

    return {
      href: branch.url,
      label: branch.name ? branch.name : 'active'
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
    return <p className="border border-dashed border-hivemind-border px-3 py-3 text-[9px] text-hivemind-dim">No links available</p>;
  }

  return (
    <div className="grid grid-cols-2 gap-px">
      {entries.map((item) => (
        <a
          key={item.key}
          href={item.resolved.href}
          target="_blank"
          rel="noreferrer"
          className="inline-flex min-w-0 items-center gap-1 border border-hivemind-border bg-[#141414] px-2 py-1.5 text-[9px] text-hivemind-muted transition-colors duration-150 hover:border-hivemind-muted hover:text-hivemind-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-hivemind-blue focus-visible:outline-offset-0"
        >
          <span className="shrink-0 text-[8px] font-bold text-hivemind-blue">{item.icon}</span>
          <span className="truncate">{item.resolved.label}</span>
          <span className="shrink-0 text-[8px] text-hivemind-dim">↗</span>
        </a>
      ))}
    </div>
  );
}

import { getProjectStatus } from './statusSystem';

export default function StatusBar({ projects }) {
  const list = Array.isArray(projects) ? projects : [];

  if (list.length === 0) {
    return (
      <div className="mb-3 border border-dashed border-hivemind-border px-3 py-2 text-[11px] uppercase tracking-[0.1em] text-hivemind-dim">
        NO UNITS
      </div>
    );
  }

  return (
    <div className="mb-3 flex w-full gap-[2px]" aria-hidden="true">
      {list.map((project) => {
        const status = getProjectStatus(project.status);
        return (
          <span
            key={project.id}
            className="h-[3px] flex-1"
            style={{
              backgroundColor: status.hex,
              opacity: project.status === 'paused' ? 0.2 : 1
            }}
            title={`${project.name ?? project.id}: ${status.label}`}
          />
        );
      })}
    </div>
  );
}

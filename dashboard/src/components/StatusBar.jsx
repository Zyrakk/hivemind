import { getProjectStatus } from './statusSystem';

export default function StatusBar({ projects }) {
  const list = Array.isArray(projects) ? projects : [];

  if (list.length === 0) {
    return (
      <div className="mt-1 border border-dashed border-hivemind-border px-2 py-1 text-[9px] uppercase tracking-[0.1em] text-hivemind-dim">
        NO UNITS
      </div>
    );
  }

  return (
    <div className="mt-1 flex w-full gap-px" aria-hidden="true">
      {list.map((project) => {
        const status = getProjectStatus(project.status);
        return (
          <span
            key={project.id}
            className="h-[2px] flex-1"
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

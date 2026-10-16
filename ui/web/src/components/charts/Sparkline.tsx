// Sparkline is a bare line over the last points of a series: no axes,
// a 1.5px stroke and an end dot. Fewer than two measured points render
// a dash. aria-hidden: the number beside it carries the meaning.
export function Sparkline({ values, width = 84, height = 22, color = "var(--color-accent)" }: { values: readonly (number | null)[]; width?: number; height?: number; color?: string }) {
  const measured = values.filter((v): v is number => v !== null);
  if (measured.length < 2) {
    return <span className="text-muted-foreground">—</span>;
  }
  const min = Math.min(...measured);
  const max = Math.max(...measured);
  const n = values.length;
  const x = (i: number) => 2 + (i * (width - 4)) / (n - 1);
  const y = (v: number) => (max === min ? height / 2 : 2 + ((max - v) * (height - 4)) / (max - min));
  let d = "";
  values.forEach((v, i) => {
    if (v === null) return;
    d += `${d ? "L" : "M"}${x(i).toFixed(1)} ${y(v).toFixed(1)} `;
  });
  const lastIndex = values.length - 1;
  const last = values[lastIndex];
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} aria-hidden="true" className="inline-block align-middle">
      <path d={d.trim()} fill="none" stroke={color} strokeWidth={1.5} />
      {last !== null && last !== undefined ? <circle cx={x(lastIndex)} cy={y(last)} r={2.5} fill={color} /> : null}
    </svg>
  );
}

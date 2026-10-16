import type { ComponentProps, ReactNode } from "react";

// The inline SVG set (DESIGN-0036 OQ4): stroke 1.8, currentColor, no
// dependency. Every icon is decorative; the text beside it names it.
type IconProps = Omit<ComponentProps<"svg">, "children">;

function Svg({ className, ...props }: IconProps & { children: ReactNode }) {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className={className ?? "size-4"} {...props} />;
}

export const icons = {
  grid: (p: IconProps) => (
    <Svg {...p}>
      <rect x="3" y="3" width="7" height="7" />
      <rect x="14" y="3" width="7" height="7" />
      <rect x="3" y="14" width="7" height="7" />
      <rect x="14" y="14" width="7" height="7" />
    </Svg>
  ),
  list: (p: IconProps) => (
    <Svg {...p}>
      <path d="M4 6h16M4 12h16M4 18h16" />
    </Svg>
  ),
  boxes: (p: IconProps) => (
    <Svg {...p}>
      <path d="M12 2 3 7v10l9 5 9-5V7z" />
      <path d="M12 12 3 7m9 5 9-5m-9 5v10" />
    </Svg>
  ),
  activity: (p: IconProps) => (
    <Svg {...p}>
      <path d="M3 12h4l3-8 4 16 3-8h4" />
    </Svg>
  ),
  check: (p: IconProps) => (
    <Svg {...p}>
      <path d="M5 12l5 5L20 7" />
    </Svg>
  ),
  book: (p: IconProps) => (
    <Svg {...p}>
      <path d="M4 4h12a3 3 0 0 1 3 3v13H7a3 3 0 0 0-3 3z" />
      <path d="M4 4v16a3 3 0 0 1 3-3h12" />
    </Svg>
  ),
  shield: (p: IconProps) => (
    <Svg {...p}>
      <path d="M12 3l7 3v5c0 5-3.5 8.5-7 10-3.5-1.5-7-5-7-10V6l7-3z" />
      <path d="M9 12l2 2 4-4" />
    </Svg>
  ),
  search: (p: IconProps) => (
    <Svg {...p}>
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" />
    </Svg>
  ),
  clock: (p: IconProps) => (
    <Svg {...p}>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </Svg>
  ),
  external: (p: IconProps) => (
    <Svg {...p}>
      <path d="M14 4h6v6M20 4l-9 9M19 14v6H4V5h6" />
    </Svg>
  ),
  menu: (p: IconProps) => (
    <Svg {...p}>
      <path d="M4 7h16M4 12h16M4 17h16" />
    </Svg>
  ),
} as const;

export type IconName = keyof typeof icons;

export function Icon({ name, ...props }: IconProps & { name: IconName }) {
  const Component = icons[name];
  return <Component {...props} />;
}

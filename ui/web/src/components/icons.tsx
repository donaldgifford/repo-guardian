import {
  IconActivity,
  IconArrowUpRight,
  IconBook,
  IconBuilding,
  IconCircleCheck,
  IconClock,
  IconLayoutGrid,
  IconListCheck,
  IconMenu2,
  IconSearch,
  IconShieldCheck,
  type IconProps as TablerProps,
} from "@tabler/icons-react";

// Tabler Icons (MIT, INV-0024) at stroke 2, the mockup's set. The names
// are ours so a view never imports Tabler directly. Every icon is
// decorative; the text beside it names it.
export const icons = {
  grid: IconLayoutGrid,
  list: IconListCheck,
  boxes: IconBuilding,
  activity: IconActivity,
  check: IconCircleCheck,
  book: IconBook,
  shield: IconShieldCheck,
  search: IconSearch,
  clock: IconClock,
  external: IconArrowUpRight,
  menu: IconMenu2,
} as const;

export type IconName = keyof typeof icons;

type IconProps = Omit<TablerProps, "size" | "stroke">;

export function Icon({ name, className, ...props }: IconProps & { name: IconName }) {
  const Component = icons[name];
  return <Component stroke={2} aria-hidden="true" className={className ?? "size-4"} {...props} />;
}

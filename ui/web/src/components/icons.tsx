import {
  IconActivity,
  IconAlertTriangle,
  IconArrowUpRight,
  IconBook,
  IconBuilding,
  IconCircleCheck,
  IconClock,
  IconGitPullRequest,
  IconLayoutGrid,
  IconListCheck,
  IconSearch,
  IconShieldCheck,
  type IconProps as TablerProps,
} from "@tabler/icons-react";

// Local, not in the design system: the app's names for its Tabler icons
// (MIT, INV-0024), at stroke 2, the mockup's set. The names are the
// app's vocabulary, so a view never imports Tabler directly; the
// package takes icons as nodes and keeps no registry of its own. Every
// icon is decorative; the text beside it names it.
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
  alert: IconAlertTriangle,
  pr: IconGitPullRequest,
} as const;

export type IconName = keyof typeof icons;

type IconProps = Omit<TablerProps, "size" | "stroke">;

export function Icon({ name, className, ...props }: IconProps & { name: IconName }) {
  const Component = icons[name];
  return <Component stroke={2} aria-hidden="true" className={className ?? "size-4"} {...props} />;
}

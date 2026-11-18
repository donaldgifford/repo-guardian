import { PageHeader as PackageHeader } from "@donaldgifford/design-system/blocks/page-header";
import type { ComponentProps } from "react";

import { cn } from "@donaldgifford/design-system/lib/utils";

// PageHeader is the design system's page header with this app's page
// spacing (DESIGN-0036 §Page anatomy): no padding of its own, since the
// frame pads the content, the actions sitting on the title's baseline,
// and endpoint names in the description set in mono.
export function PageHeader({ className, ...props }: ComponentProps<typeof PackageHeader>) {
  return <PackageHeader {...props} className={cn("mb-3.5 items-end p-0 [&_p]:text-[13px] [&_p_code]:font-mono [&_p_code]:text-xs", className)} />;
}

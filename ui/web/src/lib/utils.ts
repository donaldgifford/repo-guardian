import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

// cn joins class names, letting later Tailwind utilities win (shadcn/ui).
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

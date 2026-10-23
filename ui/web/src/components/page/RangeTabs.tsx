import { useNavigate, useSearch } from "@tanstack/react-router";

import { DAYS, type Days, DEFAULT_DAYS, parseDays, type RangeSearch } from "../../lib/range";
import { Tabs } from "../ui/tabs";

const options = DAYS.map((d) => ({ value: String(d), label: `${d}d` }));

// useRangeDays is the page's range from `?days=`, defaulted.
export function useRangeDays(): Days {
  const search = useSearch({ strict: false }) as { days?: unknown };
  return parseDays(search.days);
}

// RangeTabs is the segmented `1d | 7d | 30d | 90d` control bound to
// `?days=` as a number; the default is not written. The key is
// retained across pages by the app route's search middleware.
export function RangeTabs() {
  const days = useRangeDays();
  const navigate = useNavigate();
  const setDays = (v: string) => {
    const next = parseDays(v);
    void navigate({ to: ".", search: (prev: RangeSearch): RangeSearch => ({ ...prev, days: next === DEFAULT_DAYS ? undefined : next }) });
  };
  return <Tabs label="Range" options={options} value={String(days)} onChange={setDays} />;
}

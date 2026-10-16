import { DEFAULT_RANGE, RANGES } from "../../lib/range";
import { SearchTabs } from "../ui/tabs";

const options = RANGES.map((r) => ({ value: r, label: r }));

// RangeTabs is the segmented `1d | 7d | 30d | 90d` control bound to
// `?range=`; the default is not written.
export function RangeTabs() {
  return <SearchTabs label="Range" param="range" defaultValue={DEFAULT_RANGE} options={options} />;
}

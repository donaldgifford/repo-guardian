package budget

import "strconv"

// installationIDLabel renders an int64 installation ID as the string
// Prometheus expects for the `installation_id` label. Centralised so
// the format never drifts between metrics call sites.
func installationIDLabel(id int64) string {
	return strconv.FormatInt(id, 10)
}

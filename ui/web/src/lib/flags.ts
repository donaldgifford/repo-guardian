// flags gate panels whose data needs an API field IMPL-0031 Phase 7
// adds. A hidden panel renders nothing, never a placeholder.
export const flags = {
  // finding_age on /summary (7.3): the Finding age distribution card.
  findingAge: false,
  // aggregate=fleet|rule on /compliance/history (7.1): rule sparklines.
  ruleTrend: false,
  // PR activity heat tab on Orgs (7.5).
  prActivity: false,
  // compliance.history and the operations numbers on /status (7.4):
  // the status page's sparkline and operations strip.
  statusExtras: false,
} as const;

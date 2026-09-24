// formatTime renders an RFC3339 timestamp as "YYYY-MM-DD HH:MM:SS.sss" (UTC),
// falling back to the raw string if it doesn't parse. Shared by the deploy views.
export function formatTime(ts: string): string {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toISOString().replace("T", " ").replace("Z", "");
}

// Falls back to prep+cook for recipes saved before total_minutes existed, or manually
// edited without a total.
export function totalTimeMinutes(
  doc?: { total_minutes?: number | null; prep_minutes?: number | null; cook_minutes?: number | null } | null
): number {
  if (!doc) return 0;
  return doc.total_minutes ?? (doc.prep_minutes ?? 0) + (doc.cook_minutes ?? 0);
}

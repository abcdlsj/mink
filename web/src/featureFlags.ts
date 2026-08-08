export function designLabEnabled(
  value: string | undefined = import.meta.env.VITE_DESIGN_LAB_ENABLED,
): boolean {
  return value === "true" || value === "1";
}

/** Quote a value as a Drill string literal. */
export function quoteLiteral(value: unknown): string {
  return `'${String(value).replace(/'/g, "''")}'`;
}

export interface VariableModelLike {
  multi?: boolean;
  includeAll?: boolean;
}

/**
 * Formats template variable values like Grafana's Postgres data source: a
 * single-value variable is inserted as written (quote it in SQL yourself); a
 * multi-value or "All" variable becomes a comma-separated list of quoted
 * literals, for use in IN (...). Numbers are never quoted.
 */
export function interpolateVariable(
  value: string | number | Array<string | number>,
  variable: VariableModelLike
): string {
  if (typeof value === 'number') {
    return String(value);
  }
  if (typeof value === 'string') {
    return variable.multi || variable.includeAll ? quoteLiteral(value) : value;
  }
  if (Array.isArray(value)) {
    if (value.length === 0) {
      // An empty IN () list is a syntax error; match nothing instead.
      return 'NULL';
    }
    return value.map((v) => (typeof v === 'number' ? String(v) : quoteLiteral(v))).join(',');
  }
  return String(value);
}

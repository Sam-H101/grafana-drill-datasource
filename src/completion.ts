/**
 * Pure helpers for SQL autocomplete. No Grafana or Monaco imports, so they are
 * cheap to unit test.
 */

export interface TableRef {
  schema: string;
  table: string;
}

// FROM/JOIN followed by schema.table or schema.`table`; Drill schema names
// contain dots (dfs.tmp), so the last segment is the table.
const tableRefPattern = /\b(?:FROM|JOIN)\s+((?:[A-Za-z_][\w]*\.)*[A-Za-z_][\w]*)\.(?:`([^`]+)`|([A-Za-z_][\w]*))/gi;

/** Tables referenced in FROM and JOIN clauses, deduplicated, in order. */
export function tableRefs(sql: string): TableRef[] {
  const seen = new Set<string>();
  const out: TableRef[] = [];
  for (const m of sql.matchAll(tableRefPattern)) {
    const ref = { schema: m[1], table: m[2] ?? m[3] };
    const key = `${ref.schema}\u0000${ref.table}`;
    if (!seen.has(key)) {
      seen.add(key);
      out.push(ref);
    }
  }
  return out;
}

export type CompletionContext =
  | { kind: 'tables'; schema: string; schemaParts: string[] }
  | { kind: 'general' };

/**
 * Decides what to suggest from the text before the cursor. After
 * "dfs.tmp." it suggests the tables of dfs.tmp and the next parts of longer
 * schema names starting with "dfs.tmp." (none here); after "dfs." it suggests
 * "tmp", "root"... Otherwise schemas, columns and macros.
 */
export function completionContext(textBeforeCursor: string, schemas: string[]): CompletionContext {
  const m = textBeforeCursor.match(/((?:[A-Za-z_][\w]*\.)+)[\w]*$/);
  if (!m) {
    return { kind: 'general' };
  }
  const prefix = m[1].slice(0, -1); // "dfs.tmp"
  const lower = prefix.toLowerCase();
  const parts = new Set<string>();
  for (const s of schemas) {
    const sl = s.toLowerCase();
    if (sl.startsWith(lower + '.')) {
      parts.add(s.slice(prefix.length + 1).split('.')[0]);
    }
  }
  const isSchema = schemas.some((s) => s.toLowerCase() === lower);
  if (!isSchema && parts.size === 0) {
    return { kind: 'general' };
  }
  return { kind: 'tables', schema: isSchema ? prefix : '', schemaParts: [...parts] };
}

/** Backtick a table name when it is not a plain identifier (files: data.csv). */
export function quoteIdentifier(name: string): string {
  return /^[A-Za-z_][\w]*$/.test(name) ? name : '`' + name.replace(/`/g, '``') + '`';
}

export const MACROS: Array<{ label: string; insert: string; detail: string }> = [
  { label: '$__timeFilter', insert: '\\$__timeFilter(${1:column})', detail: 'Dashboard time range predicate' },
  { label: '$__timeFrom', insert: '\\$__timeFrom()', detail: 'Range start as TIMESTAMP' },
  { label: '$__timeTo', insert: '\\$__timeTo()', detail: 'Range end as TIMESTAMP' },
  { label: '$__timeGroup', insert: '\\$__timeGroup(${1:column}, ${2:\\$__interval})', detail: 'Time bucket' },
  { label: '$__timeGroupAlias', insert: '\\$__timeGroupAlias(${1:column}, ${2:\\$__interval})', detail: 'Time bucket AS `time`' },
  { label: '$__unixEpochFilter', insert: '\\$__unixEpochFilter(${1:column})', detail: 'Range predicate on epoch seconds' },
  { label: '$__interval', insert: '\\$__interval', detail: 'Panel interval' },
];

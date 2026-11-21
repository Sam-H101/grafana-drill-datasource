/**
 * Statement safety for the query editor's warning. Mirrors pkg/sqlguard, which
 * is what actually enforces the policy: the backend refuses blocked statements
 * whatever the browser does. Both are tested against
 * pkg/sqlguard/testdata/cases.json.
 */

export type StatementKind = 'read' | 'ddl' | 'dml' | 'options' | 'unknown';

/** Which kinds of statement other than queries the data source allows. */
export interface StatementPolicy {
  allowDDL: boolean;
  allowDML: boolean;
  allowOptions: boolean;
}

const KEYWORD_KINDS: Record<string, StatementKind> = {
  SELECT: 'read',
  WITH: 'read',
  VALUES: 'read',
  TABLE: 'read',
  SHOW: 'read',
  DESCRIBE: 'read',
  DESC: 'read',
  EXPLAIN: 'read',

  CREATE: 'ddl',
  DROP: 'ddl',
  ALTER: 'ddl',
  REFRESH: 'ddl',
  ANALYZE: 'ddl',

  INSERT: 'dml',
  UPDATE: 'dml',
  DELETE: 'dml',
  MERGE: 'dml',
  UPSERT: 'dml',
  TRUNCATE: 'dml',

  SET: 'options',
  RESET: 'options',
  USE: 'options',
};

function isCommentStart(s: string, i: number): boolean {
  const two = s.slice(i, i + 2);
  return two === '--' || two === '//' || two === '/*';
}

/** Index after the comment starting at i; an unterminated comment ends the text. */
function skipComment(s: string, i: number): number {
  if (s.startsWith('/*', i)) {
    const end = s.indexOf('*/', i + 2);
    return end < 0 ? s.length : end + 2;
  }
  const end = s.indexOf('\n', i);
  return end < 0 ? s.length : end + 1;
}

/** Index after the quoted run starting at i; a doubled quote is an escape. */
function skipQuoted(s: string, i: number, q: string): number {
  for (let j = i + 1; j < s.length; j++) {
    if (s[j] === q) {
      if (s[j + 1] === q) {
        j++;
        continue;
      }
      return j + 1;
    }
  }
  return s.length;
}

/** Skips ASCII whitespace and comments (and, with parens, opening parentheses). */
function skipLeading(s: string, start: number, parens: boolean): number {
  let i = start;
  while (i < s.length) {
    const c = s[i];
    if (c === ' ' || c === '\t' || c === '\n' || c === '\r' || c === '\f') {
      i++;
    } else if (parens && c === '(') {
      i++;
    } else if (isCommentStart(s, i)) {
      i = skipComment(s, i);
    } else {
      break;
    }
  }
  return i;
}

function readWord(s: string, i: number): [string, number] {
  let j = i;
  while (j < s.length && /[A-Za-z_]/.test(s[j])) {
    j++;
  }
  return [s.slice(i, j), j];
}

/** The kind of a statement and its leading keyword in upper case ('' if none). */
export function classify(sql: string): { kind: StatementKind; keyword: string } {
  const [word, next] = readWord(sql, skipLeading(sql, 0, true));
  if (!word) {
    return { kind: 'unknown', keyword: '' };
  }
  const keyword = word.toUpperCase();
  const kind = Object.prototype.hasOwnProperty.call(KEYWORD_KINDS, keyword) ? KEYWORD_KINDS[keyword] : undefined;
  if (!kind) {
    return { kind: 'unknown', keyword };
  }
  if (keyword === 'ALTER') {
    const second = readWord(sql, skipLeading(sql, next, false))[0].toUpperCase();
    if (second === 'SYSTEM' || second === 'SESSION') {
      return { kind: 'options', keyword: `${keyword} ${second}` };
    }
  }
  return { kind, keyword };
}

/** Whether a semicolon outside strings, quoted identifiers and comments is followed by more SQL. */
export function hasMultipleStatements(sql: string): boolean {
  for (let i = 0; i < sql.length; ) {
    const c = sql[i];
    if (c === "'" || c === '"' || c === '`') {
      i = skipQuoted(sql, i, c);
    } else if (isCommentStart(sql, i)) {
      i = skipComment(sql, i);
    } else if (c === ';') {
      if (skipLeading(sql, i + 1, false) < sql.length) {
        return true;
      }
      i++;
    } else {
      i++;
    }
  }
  return false;
}

const WHAT: Record<Exclude<StatementKind, 'read' | 'unknown'>, string> = {
  ddl: 'creating, altering or dropping tables, views, schemas or functions',
  dml: 'inserting, updating or deleting data',
  options: 'changing Drill options or session state',
};

/**
 * Why the data source will refuse this SQL, or null if it will run it. Text
 * with nothing to run yet (empty, or only comments) gives null.
 */
export function blockedReason(sql: string, policy: StatementPolicy): string | null {
  if (policy.allowDDL && policy.allowDML && policy.allowOptions) {
    return null;
  }
  if (skipLeading(sql, 0, true) >= sql.length) {
    return null;
  }
  if (hasMultipleStatements(sql)) {
    return 'Only one statement can be run at a time: remove everything after the first semicolon.';
  }
  const { kind, keyword } = classify(sql);
  switch (kind) {
    case 'read':
      return null;
    case 'ddl':
      return policy.allowDDL ? null : `${keyword} statements are blocked: this data source does not allow ${WHAT.ddl}.`;
    case 'dml':
      return policy.allowDML ? null : `${keyword} statements are blocked: this data source does not allow ${WHAT.dml}.`;
    case 'options':
      return policy.allowOptions
        ? null
        : `${keyword} statements are blocked: this data source does not allow ${WHAT.options}.`;
    default: {
      // Often a typo in the first word, so say what was read and what is expected.
      const word = keyword.length > 32 ? `${keyword.slice(0, 32)}…` : keyword;
      return `${word ? `"${word}" is` : 'This is'} not a statement this data source runs: it runs queries (SELECT, WITH, VALUES, SHOW, DESCRIBE, EXPLAIN) and the kinds of statement its settings allow. Check the first word of the statement.`;
    }
  }
}

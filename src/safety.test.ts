import { readFileSync } from 'fs';
import { join } from 'path';
import { blockedReason, classify, hasMultipleStatements } from './safety';

// The same cases test pkg/sqlguard, which enforces the policy in the backend.
const cases: {
  classify: Array<{ sql: string; kind: string; keyword: string }>;
  multiple: Array<{ sql: string; multiple: boolean }>;
  check: Array<{ sql: string; ddl: boolean; dml: boolean; options: boolean; allowed: boolean }>;
} = JSON.parse(readFileSync(join(__dirname, '..', 'pkg', 'sqlguard', 'testdata', 'cases.json'), 'utf8'));

describe('statement safety (shared cases with pkg/sqlguard)', () => {
  it('has cases to run', () => {
    expect(cases.classify.length).toBeGreaterThan(40);
    expect(cases.multiple.length).toBeGreaterThan(10);
    expect(cases.check.length).toBeGreaterThan(10);
  });

  it.each(cases.classify)('classifies $sql', ({ sql, kind, keyword }) => {
    expect(classify(sql)).toEqual({ kind, keyword });
  });

  it.each(cases.multiple)('multiple statements in $sql', ({ sql, multiple }) => {
    expect(hasMultipleStatements(sql)).toBe(multiple);
  });

  it.each(cases.check)('checks $sql (ddl=$ddl dml=$dml options=$options)', ({ sql, ddl, dml, options, allowed }) => {
    const reason = blockedReason(sql, { allowDDL: ddl, allowDML: dml, allowOptions: options });
    expect(reason === null).toBe(allowed);
  });
});

describe('blockedReason', () => {
  const readOnly = { allowDDL: false, allowDML: false, allowOptions: false };

  it('names the keyword and what is not allowed', () => {
    expect(blockedReason('drop table dfs.tmp.t', readOnly)).toMatch(/^DROP statements are blocked: .*dropping/);
    expect(blockedReason('DELETE FROM t', readOnly)).toMatch(/^DELETE statements are blocked: .*deleting data/);
    expect(blockedReason('ALTER SYSTEM SET `a` = 1', readOnly)).toMatch(/^ALTER SYSTEM statements are blocked/);
    expect(blockedReason('SELECT 1; DROP TABLE t', readOnly)).toMatch(/^Only one statement/);
  });

  it('treats an unrecognised first word as a likely typo', () => {
    expect(blockedReason('SELEC 1', readOnly)).toMatch(/^"SELEC" is not a statement this data source runs/);
    expect(blockedReason('1 + 1', readOnly)).toMatch(/^This is not a statement/);
    expect(blockedReason(`${'A'.repeat(500)} 1`, readOnly)!.length).toBeLessThan(400);
  });

  it('does not warn while there is nothing to run', () => {
    expect(blockedReason('', readOnly)).toBeNull();
    expect(blockedReason('  \n', readOnly)).toBeNull();
    expect(blockedReason('-- just a note', readOnly)).toBeNull();
  });

  it('does not quote the SQL', () => {
    expect(blockedReason('DROP TABLE secret_table', readOnly)).not.toContain('secret_table');
  });
});

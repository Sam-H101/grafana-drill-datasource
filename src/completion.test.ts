import { completionContext, quoteIdentifier, tableRefs } from './completion';

const schemas = ['cp.default', 'dfs.default', 'dfs.root', 'dfs.tmp', 'information_schema', 'sys'];

describe('tableRefs', () => {
  it('finds schema.table and schema.`file` references', () => {
    expect(
      tableRefs('SELECT * FROM dfs.tmp.`data.csv` a JOIN sys.version v ON 1=1 JOIN dfs.tmp.`data.csv` b ON 1=1')
    ).toEqual([
      { schema: 'dfs.tmp', table: 'data.csv' },
      { schema: 'sys', table: 'version' },
    ]);
  });

  it('ignores unqualified tables', () => {
    expect(tableRefs('SELECT 1 FROM t')).toEqual([]);
  });
});

describe('completionContext', () => {
  it('suggests tables after a full schema name', () => {
    expect(completionContext('SELECT * FROM dfs.tmp.', schemas)).toEqual({ kind: 'tables', schema: 'dfs.tmp', schemaParts: [] });
    expect(completionContext('FROM sys.ver', schemas)).toEqual({ kind: 'tables', schema: 'sys', schemaParts: [] });
  });

  it('suggests the next schema part after a partial schema name', () => {
    const ctx = completionContext('FROM dfs.', schemas);
    expect(ctx.kind).toBe('tables');
    if (ctx.kind === 'tables') {
      expect(ctx.schema).toBe('');
      expect(ctx.schemaParts.sort()).toEqual(['default', 'root', 'tmp']);
    }
  });

  it('falls back to general suggestions', () => {
    expect(completionContext('SELECT ', schemas)).toEqual({ kind: 'general' });
    expect(completionContext('SELECT t.', schemas)).toEqual({ kind: 'general' });
  });
});

describe('quoteIdentifier', () => {
  it('backticks names that are not plain identifiers', () => {
    expect(quoteIdentifier('version')).toBe('version');
    expect(quoteIdentifier('data.csv')).toBe('`data.csv`');
    expect(quoteIdentifier('we`ird')).toBe('`we``ird`');
  });
});

import { interpolateVariable, quoteLiteral } from './interpolate';

describe('quoteLiteral', () => {
  it('escapes single quotes by doubling them', () => {
    expect(quoteLiteral("O'Brien")).toBe("'O''Brien'");
    expect(quoteLiteral("'; DROP TABLE x; --")).toBe("'''; DROP TABLE x; --'");
  });
});

describe('interpolateVariable', () => {
  it('inserts single values as written', () => {
    expect(interpolateVariable('M', { multi: false, includeAll: false })).toBe('M');
  });

  it('quotes a single value when the variable is multi-value or has All', () => {
    expect(interpolateVariable("it's", { multi: true })).toBe("'it''s'");
    expect(interpolateVariable('x', { includeAll: true })).toBe("'x'");
  });

  it('turns arrays into a quoted, comma-separated list', () => {
    expect(interpolateVariable(['M', "F'x"], { multi: true })).toBe("'M','F''x'");
  });

  it('never quotes numbers', () => {
    expect(interpolateVariable(5, { multi: true })).toBe('5');
    expect(interpolateVariable([1, 'a'], { multi: true })).toBe("1,'a'");
  });

  it('makes an empty selection match nothing instead of breaking IN ()', () => {
    expect(interpolateVariable([], { multi: true })).toBe('NULL');
  });
});

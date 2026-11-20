import {
  createDataFrame,
  CustomVariableSupport,
  DataFrame,
  DataQueryRequest,
  DataQueryResponse,
  Field,
  FieldType,
} from '@grafana/data';
import { Observable, map } from 'rxjs';
import type { DataSource } from './datasource';
import { VariableQueryEditor } from './components/VariableQueryEditor';
import { DrillQuery, DrillVariableQuery } from './types';

/**
 * Turns a query result into the text/value frame Grafana builds variable
 * options from. Columns named __text and __value give the label and value;
 * otherwise the first column gives both.
 */
export function toTextValueFrame(frame: DataFrame): DataFrame {
  const find = (name: string) => frame.fields.find((f: Field) => f.name.toLowerCase() === name);
  const first = frame.fields[0];
  const textField = find('__text') ?? find('__value') ?? first;
  const valueField = find('__value') ?? textField;
  const asStrings = (f?: Field) => (f ? f.values.map((v: unknown) => (v === null || v === undefined ? '' : String(v))) : []);
  return createDataFrame({
    refId: frame.refId,
    fields: [
      { name: 'text', type: FieldType.string, values: asStrings(textField) },
      { name: 'value', type: FieldType.string, values: asStrings(valueField) },
    ],
  });
}

export class DrillVariableSupport extends CustomVariableSupport<DataSource, DrillVariableQuery, DrillQuery> {
  editor = VariableQueryEditor;

  constructor(private readonly datasource: DataSource) {
    super();
  }

  query(request: DataQueryRequest<DrillVariableQuery>): Observable<DataQueryResponse> {
    const targets: DrillQuery[] = request.targets.map((t) => ({
      refId: t.refId || 'variable',
      rawSql: t.rawSql ?? '',
      format: 'table',
    }));
    return this.datasource
      .query({ ...request, targets })
      .pipe(map((resp) => ({ ...resp, data: resp.data.map((f: DataFrame) => toTextValueFrame(f)) })));
  }
}

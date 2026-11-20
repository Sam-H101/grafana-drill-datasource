import { CoreApp, DataSourceInstanceSettings, ScopedVars } from '@grafana/data';
import { DataSourceWithBackend, getTemplateSrv } from '@grafana/runtime';
import { interpolateVariable } from './interpolate';
import { DEFAULT_QUERY, DrillOptions, DrillQuery } from './types';
import { DrillVariableSupport } from './variables';

export interface ColumnInfo {
  name: string;
  type: string;
}

export class DataSource extends DataSourceWithBackend<DrillQuery, DrillOptions> {
  constructor(instanceSettings: DataSourceInstanceSettings<DrillOptions>) {
    super(instanceSettings);
    this.variables = new DrillVariableSupport(this);
    // Standard annotation support: the query editor is reused and result
    // columns (time, timeEnd, text, tags) are mapped by Grafana.
    this.annotations = {};
  }

  getDefaultQuery(_: CoreApp): Partial<DrillQuery> {
    return DEFAULT_QUERY;
  }

  filterQuery(query: DrillQuery): boolean {
    return !query.hide && !!query.rawSql?.trim();
  }

  applyTemplateVariables(query: DrillQuery, scopedVars: ScopedVars): DrillQuery {
    const templateSrv = getTemplateSrv();
    return {
      ...query,
      rawSql: templateSrv.replace(query.rawSql ?? '', scopedVars, interpolateVariable),
      defaultSchema: query.defaultSchema ? templateSrv.replace(query.defaultSchema, scopedVars) : query.defaultSchema,
    };
  }

  getSchemas(): Promise<string[]> {
    return this.getResource<string[]>('schemas');
  }

  getTables(schema: string): Promise<string[]> {
    return this.getResource<string[]>('tables', { schema });
  }

  getColumns(schema: string, table: string): Promise<ColumnInfo[]> {
    return this.getResource<ColumnInfo[]>('columns', { schema, table });
  }
}

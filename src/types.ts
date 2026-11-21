import { DataSourceJsonData } from '@grafana/data';
import { DataQuery } from '@grafana/schema';

export type Format = 'table' | 'timeseries';
export type FillMode = '' | 'null' | 'previous' | 'value';

/** One panel query. Mirrors pkg/models/query.go. */
export interface DrillQuery extends DataQuery {
  rawSql?: string;
  format?: Format;
  /** Overrides the data source's default schema for this query. */
  defaultSchema?: string;
  fillMode?: FillMode;
  fillValue?: number;
}

export const DEFAULT_QUERY: Partial<DrillQuery> = {
  rawSql: '',
  format: 'table',
};

export type AuthMode = 'none' | 'basic' | 'form';

/** Data source settings (jsonData). The URL is Grafana's standard url field. */
export interface DrillOptions extends DataSourceJsonData {
  authMode?: AuthMode;
  username?: string;
  impersonateUser?: string;
  defaultSchema?: string;
  timeoutSeconds?: number;
  autoLimit?: number;
  // Statement safety: only queries run unless these are turned on. Enforced
  // by the backend (pkg/sqlguard).
  allowDDL?: boolean;
  allowDML?: boolean;
  allowOptionChanges?: boolean;
  // Read by the Grafana SDK's HTTP client.
  enableSecureSocksProxy?: boolean;
  tlsSkipVerify?: boolean;
  tlsAuth?: boolean;
  tlsAuthWithCACert?: boolean;
  serverName?: string;
}

/** Secrets: write-only from the browser, decrypted only in the backend. */
export interface DrillSecureJsonData {
  password?: string;
  tlsCACert?: string;
  tlsClientCert?: string;
  tlsClientKey?: string;
}

/** A template variable query. */
export interface DrillVariableQuery extends DataQuery {
  rawSql?: string;
}

import React, { ChangeEvent } from 'react';
import {
  DataSourcePluginOptionsEditorProps,
  onUpdateDatasourceJsonDataOption,
  onUpdateDatasourceSecureJsonDataOption,
  updateDatasourcePluginResetOption,
} from '@grafana/data';
import { config } from '@grafana/runtime';
import {
  Alert,
  Field,
  Input,
  RadioButtonGroup,
  SecretInput,
  SecretTextArea,
  SecureSocksProxySettings,
  Stack,
  Switch,
  Text,
} from '@grafana/ui';
import { AuthMode, DrillOptions, DrillSecureJsonData } from '../types';

type Props = DataSourcePluginOptionsEditorProps<DrillOptions, DrillSecureJsonData>;

const authOptions: Array<{ label: string; value: AuthMode; description: string }> = [
  { label: 'None', value: 'none', description: 'Drill has user authentication turned off' },
  { label: 'Basic', value: 'basic', description: 'HTTP Basic; needs BASIC in drill.exec.http.auth.mechanisms' },
  { label: 'Form login', value: 'form', description: 'Logs in like the Drill web UI (FORM mechanism)' },
];

const WIDTH = 50;

export function ConfigEditor(props: Props) {
  const { options, onOptionsChange } = props;
  const { jsonData, secureJsonFields } = options;
  const authMode: AuthMode = jsonData.authMode ?? 'none';
  const plainHttpWithCredentials = authMode !== 'none' && options.url?.trim().toLowerCase().startsWith('http://');

  const setJson = <K extends keyof DrillOptions>(key: K, value: DrillOptions[K]) =>
    onOptionsChange({ ...options, jsonData: { ...jsonData, [key]: value } });

  const onNumber = (key: 'timeoutSeconds' | 'autoLimit') => (e: ChangeEvent<HTMLInputElement>) => {
    const n = parseInt(e.currentTarget.value, 10);
    setJson(key, Number.isFinite(n) ? n : undefined);
  };

  return (
    <Stack direction="column" gap={3}>
      <section>
        <Text element="h3">Connection</Text>
        <Field
          label="URL"
          description="Base URL of Drill's web port, for example http://drill.example.com:8047"
          required
        >
          <Input
            id="drill-url"
            width={WIDTH}
            value={options.url || ''}
            placeholder="http://localhost:8047"
            onChange={(e) => onOptionsChange({ ...options, url: e.currentTarget.value })}
          />
        </Field>
        {config.secureSocksDSProxyEnabled && (
          <div data-testid="drill-secure-socks">
            <SecureSocksProxySettings options={options} onOptionsChange={onOptionsChange} />
          </div>
        )}
      </section>

      <section>
        <Text element="h3">Authentication</Text>
        <Field label="Authentication mode">
          <RadioButtonGroup
            id="drill-auth-mode"
            options={authOptions}
            value={authMode}
            onChange={(v) => setJson('authMode', v)}
          />
        </Field>

        {authMode === 'none' ? (
          <Field
            label="Impersonate user"
            description="Optional. Runs queries as this Drill user (User-Name header). Drill needs drill.exec.impersonation.enabled."
          >
            <Input
              id="drill-impersonate-user"
              width={WIDTH}
              value={jsonData.impersonateUser || ''}
              onChange={onUpdateDatasourceJsonDataOption(props, 'impersonateUser')}
            />
          </Field>
        ) : (
          <>
            <Field label="Username" required>
              <Input
                id="drill-username"
                width={WIDTH}
                autoComplete="off"
                value={jsonData.username || ''}
                onChange={onUpdateDatasourceJsonDataOption(props, 'username')}
              />
            </Field>
            <Field label="Password" required>
              <SecretInput
                id="drill-password"
                width={WIDTH}
                autoComplete="new-password"
                isConfigured={!!secureJsonFields?.password}
                value={options.secureJsonData?.password || ''}
                onChange={onUpdateDatasourceSecureJsonDataOption(props, 'password')}
                onReset={() => updateDatasourcePluginResetOption(props, 'password')}
              />
            </Field>
            {plainHttpWithCredentials && (
              <Alert severity="warning" title="Credentials over plain HTTP">
                The URL uses http://, so the password is sent unencrypted. Use https:// unless Grafana and Drill share a
                trusted network.
              </Alert>
            )}
          </>
        )}
      </section>

      <section>
        <Text element="h3">TLS</Text>
        <Field label="Skip TLS certificate verification" description="Insecure. Only for testing.">
          <Switch
            id="drill-tls-skip-verify"
            value={!!jsonData.tlsSkipVerify}
            onChange={(e) => setJson('tlsSkipVerify', e.currentTarget.checked)}
          />
        </Field>
        <Field label="Custom CA certificate" description="Verify Drill's certificate against this CA (PEM).">
          <Switch
            id="drill-tls-with-ca"
            value={!!jsonData.tlsAuthWithCACert}
            onChange={(e) => setJson('tlsAuthWithCACert', e.currentTarget.checked)}
          />
        </Field>
        {jsonData.tlsAuthWithCACert && (
          <Field label="CA certificate">
            <SecretTextArea
              id="drill-tls-ca"
              cols={60}
              rows={6}
              placeholder="-----BEGIN CERTIFICATE-----"
              isConfigured={!!secureJsonFields?.tlsCACert}
              onChange={onUpdateDatasourceSecureJsonDataOption(props, 'tlsCACert')}
              onReset={() => updateDatasourcePluginResetOption(props, 'tlsCACert')}
            />
          </Field>
        )}
        <Field label="Client certificate" description="Present a client certificate to Drill (mutual TLS).">
          <Switch
            id="drill-tls-client-auth"
            value={!!jsonData.tlsAuth}
            onChange={(e) => setJson('tlsAuth', e.currentTarget.checked)}
          />
        </Field>
        {jsonData.tlsAuth && (
          <>
            <Field label="Server name" description="Expected name in Drill's certificate, if it differs from the URL host.">
              <Input
                id="drill-tls-server-name"
                width={WIDTH}
                value={jsonData.serverName || ''}
                onChange={onUpdateDatasourceJsonDataOption(props, 'serverName')}
              />
            </Field>
            <Field label="Client certificate (PEM)">
              <SecretTextArea
                id="drill-tls-client-cert"
                cols={60}
                rows={6}
                placeholder="-----BEGIN CERTIFICATE-----"
                isConfigured={!!secureJsonFields?.tlsClientCert}
                onChange={onUpdateDatasourceSecureJsonDataOption(props, 'tlsClientCert')}
                onReset={() => updateDatasourcePluginResetOption(props, 'tlsClientCert')}
              />
            </Field>
            <Field label="Client key (PEM)">
              <SecretTextArea
                id="drill-tls-client-key"
                cols={60}
                rows={6}
                placeholder="-----BEGIN PRIVATE KEY-----"
                isConfigured={!!secureJsonFields?.tlsClientKey}
                onChange={onUpdateDatasourceSecureJsonDataOption(props, 'tlsClientKey')}
                onReset={() => updateDatasourcePluginResetOption(props, 'tlsClientKey')}
              />
            </Field>
          </>
        )}
      </section>

      <section>
        <Text element="h3">Statement safety</Text>
        <Text element="p" color="secondary">
          Queries (SELECT, WITH, VALUES, SHOW, DESCRIBE, EXPLAIN) always run. Every other statement is refused before it
          reaches Drill unless you allow its kind here. This guards against mistakes; also give Grafana a Drill user
          that can only read the data it needs.
        </Text>
        <Field
          label="Allow creating and dropping objects"
          description="CREATE, DROP, ALTER, REFRESH and ANALYZE: tables, views, schemas, functions, metadata."
        >
          <Switch
            id="drill-allow-ddl"
            value={!!jsonData.allowDDL}
            onChange={(e) => setJson('allowDDL', e.currentTarget.checked)}
          />
        </Field>
        <Field label="Allow changing data" description="INSERT, UPDATE, DELETE, MERGE and TRUNCATE.">
          <Switch
            id="drill-allow-dml"
            value={!!jsonData.allowDML}
            onChange={(e) => setJson('allowDML', e.currentTarget.checked)}
          />
        </Field>
        <Field
          label="Allow changing Drill options"
          description="ALTER SYSTEM, ALTER SESSION, SET, RESET and USE. ALTER SYSTEM affects every user of the Drill cluster."
        >
          <Switch
            id="drill-allow-options"
            value={!!jsonData.allowOptionChanges}
            onChange={(e) => setJson('allowOptionChanges', e.currentTarget.checked)}
          />
        </Field>
        {jsonData.allowDDL && jsonData.allowDML && jsonData.allowOptionChanges && (
          <Alert severity="warning" title="No statement is blocked">
            With all three allowed, anyone who can query this data source can run any statement the Drill user is
            permitted to run.
          </Alert>
        )}
      </section>

      <section>
        <Text element="h3">Query defaults</Text>
        <Field label="Default schema" description="Schema for unqualified table names, for example dfs.tmp">
          <Input
            id="drill-default-schema"
            width={WIDTH}
            value={jsonData.defaultSchema || ''}
            onChange={onUpdateDatasourceJsonDataOption(props, 'defaultSchema')}
          />
        </Field>
        <Field label="Query timeout (seconds)" description="Queries running longer are cancelled on Drill. Default 60.">
          <Input
            id="drill-timeout"
            type="number"
            min={1}
            max={3600}
            width={20}
            placeholder="60"
            value={jsonData.timeoutSeconds ?? ''}
            onChange={onNumber('timeoutSeconds')}
          />
        </Field>
        <Field label="Row limit" description="Maximum rows returned per query. Default 10000.">
          <Input
            id="drill-row-limit"
            type="number"
            min={1}
            max={1000000}
            width={20}
            placeholder="10000"
            value={jsonData.autoLimit ?? ''}
            onChange={onNumber('autoLimit')}
          />
        </Field>
      </section>
    </Stack>
  );
}

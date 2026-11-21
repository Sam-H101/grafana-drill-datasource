import React, { useCallback, useEffect, useRef, useState } from 'react';
import { QueryEditorProps, SelectableValue } from '@grafana/data';
import {
  Alert,
  Button,
  CodeEditor,
  Collapse,
  Combobox,
  ComboboxOption,
  InlineField,
  Input,
  RadioButtonGroup,
  Stack,
  Text,
} from '@grafana/ui';
import { DataSource } from '../datasource';
import { completionContext, MACROS, quoteIdentifier, tableRefs } from '../completion';
import { blockedReason } from '../safety';
import { DrillOptions, DrillQuery, FillMode, Format } from '../types';
import { MacroHelp } from './MacroHelp';

type Props = QueryEditorProps<DataSource, DrillQuery, DrillOptions>;

const formatOptions: Array<SelectableValue<Format>> = [
  { label: 'Table', value: 'table' },
  { label: 'Time series', value: 'timeseries' },
];

const fillOptions: Array<ComboboxOption<FillMode>> = [
  { label: 'No fill', value: '' },
  { label: 'Null', value: 'null' },
  { label: 'Previous value', value: 'previous' },
  { label: 'Fixed value', value: 'value' },
];

async function safe<T>(p: Promise<T>, fallback: T): Promise<T> {
  try {
    return await p;
  } catch {
    return fallback;
  }
}

export function QueryEditor({ query, onChange, onRunQuery, datasource }: Props) {
  const [helpOpen, setHelpOpen] = useState(false);
  // Monaco callbacks are registered once; keep the latest query in a ref.
  const latest = useRef(query);
  useEffect(() => {
    latest.current = query;
  }, [query]);

  // Schema list, fetched once per editor (the backend also caches).
  const schemasRef = useRef<Promise<string[]> | null>(null);
  const getSchemas = useCallback(() => {
    if (!schemasRef.current) {
      schemasRef.current = safe(datasource.getSchemas(), []);
    }
    return schemasRef.current;
  }, [datasource]);

  const completionDisposable = useRef<{ dispose(): void } | null>(null);
  useEffect(() => () => completionDisposable.current?.dispose(), []);

  const setSql = useCallback(
    (rawSql: string) => {
      if (rawSql !== latest.current.rawSql) {
        onChange({ ...latest.current, rawSql });
      }
    },
    [onChange]
  );

  const runWith = useCallback(
    (rawSql: string) => {
      onChange({ ...latest.current, rawSql });
      onRunQuery();
    },
    [onChange, onRunQuery]
  );

  const loadSchemaOptions = useCallback(
    async (input: string): Promise<Array<ComboboxOption<string>>> => {
      const schemas = await getSchemas();
      const needle = input.toLowerCase();
      return schemas.filter((s) => s.toLowerCase().includes(needle)).map((s) => ({ label: s, value: s }));
    },
    [getSchemas]
  );

  // The backend refuses these statements; say so while the SQL is being
  // typed. The editor commits its text to the query only on blur or run, so
  // keep what was typed since the last commit; a query changed from outside
  // (a different base) wins over it.
  const savedSql = query.rawSql ?? '';
  const [draft, setDraft] = useState({ base: savedSql, text: savedSql });
  const editorSql = draft.base === savedSql ? draft.text : savedSql;
  const blocked = blockedReason(editorSql, datasource.statementPolicy);

  return (
    <Stack direction="column" gap={1}>
      <div data-testid="drill-sql-editor" aria-label="SQL query">
        <CodeEditor
          language="sql"
          value={query.rawSql ?? ''}
          height={180}
          showLineNumbers
          showMiniMap={false}
          onBlur={setSql}
          onChange={(text) => setDraft({ base: latest.current.rawSql ?? '', text })}
          onSave={runWith}
          onEditorDidMount={(editor, monaco) => {
            // Ctrl/Cmd+Enter runs the query.
            editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => runWith(editor.getValue()));

            // Providers are global per language: answer only for this editor.
            completionDisposable.current?.dispose();
            completionDisposable.current = monaco.languages.registerCompletionItemProvider('sql', {
              triggerCharacters: ['.', ' ', '$'],
              provideCompletionItems: async (model, position) => {
                if (model.uri.toString() !== editor.getModel()?.uri.toString()) {
                  return { suggestions: [] };
                }
                const word = model.getWordUntilPosition(position);
                const lineBefore = model.getValueInRange({
                  startLineNumber: position.lineNumber,
                  startColumn: 1,
                  endLineNumber: position.lineNumber,
                  endColumn: position.column,
                });
                const startColumn = lineBefore.charAt(word.startColumn - 2) === '$' ? word.startColumn - 1 : word.startColumn;
                const range = {
                  startLineNumber: position.lineNumber,
                  endLineNumber: position.lineNumber,
                  startColumn,
                  endColumn: word.endColumn,
                };
                const K = monaco.languages.CompletionItemKind;
                const schemas = await getSchemas();
                const ctx = completionContext(lineBefore, schemas);

                if (ctx.kind === 'tables') {
                  const tables = ctx.schema ? await safe(datasource.getTables(ctx.schema), []) : [];
                  return {
                    suggestions: [
                      ...ctx.schemaParts.map((p) => ({ label: p, kind: K.Module, insertText: p, range, detail: 'schema' })),
                      ...tables.map((t) => ({ label: t, kind: K.Struct, insertText: quoteIdentifier(t), range, detail: 'table' })),
                    ],
                  };
                }

                const refs = tableRefs(model.getValue()).slice(0, 5);
                const columns = (await Promise.all(refs.map((r) => safe(datasource.getColumns(r.schema, r.table), [])))).flat();
                return {
                  suggestions: [
                    ...schemas.map((s) => ({ label: s, kind: K.Module, insertText: s, range, detail: 'schema' })),
                    ...columns.map((c) => ({
                      label: c.name,
                      kind: K.Field,
                      insertText: quoteIdentifier(c.name),
                      range,
                      detail: c.type,
                    })),
                    ...MACROS.map((m) => ({
                      label: m.label,
                      kind: K.Function,
                      insertText: m.insert,
                      insertTextRules: monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet,
                      range,
                      detail: m.detail,
                    })),
                  ],
                };
              },
            });
          }}
        />
      </div>
      {blocked && (
        <Alert severity="warning" title="This statement will not run" data-testid="drill-statement-blocked">
          {blocked} An administrator can change this under Statement safety in the data source settings.
        </Alert>
      )}
      <Stack direction="row" gap={2} wrap="wrap" alignItems="center">
        <InlineField label="Format" labelWidth={10}>
          <RadioButtonGroup
            options={formatOptions}
            value={query.format ?? 'table'}
            onChange={(format) => {
              onChange({ ...query, format });
              onRunQuery();
            }}
          />
        </InlineField>
        {query.format === 'timeseries' && (
          <>
            <InlineField label="Fill" labelWidth={8} tooltip="How to fill gaps when series are split by label columns">
              <Combobox
                id="drill-fill-mode"
                width={20}
                options={fillOptions}
                value={query.fillMode ?? ''}
                onChange={(o) => {
                  onChange({ ...query, fillMode: o.value });
                  onRunQuery();
                }}
              />
            </InlineField>
            {query.fillMode === 'value' && (
              <InlineField label="Value" labelWidth={8}>
                <Input
                  id="drill-fill-value"
                  type="number"
                  width={12}
                  value={query.fillValue ?? 0}
                  onChange={(e) => onChange({ ...query, fillValue: parseFloat(e.currentTarget.value) || 0 })}
                  onBlur={onRunQuery}
                />
              </InlineField>
            )}
          </>
        )}
        <InlineField label="Schema" labelWidth={10} tooltip="Overrides the data source's default schema for this query">
          <Combobox
            id="drill-query-schema"
            width={28}
            placeholder="default"
            isClearable
            createCustomValue
            options={loadSchemaOptions}
            value={query.defaultSchema ? { label: query.defaultSchema, value: query.defaultSchema } : null}
            onChange={(o) => {
              onChange({ ...query, defaultSchema: o?.value ?? '' });
              onRunQuery();
            }}
          />
        </InlineField>
        <Button icon="play" variant="primary" size="sm" onClick={onRunQuery}>
          Run query
        </Button>
        <Text variant="bodySmall" color="secondary">
          Ctrl+Enter to run
        </Text>
      </Stack>
      <Collapse label="Macros" isOpen={helpOpen} onToggle={() => setHelpOpen(!helpOpen)}>
        <MacroHelp />
      </Collapse>
    </Stack>
  );
}

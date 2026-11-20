import React, { useState } from 'react';
import { QueryEditorProps } from '@grafana/data';
import { Field, Stack, Text, TextArea } from '@grafana/ui';
import type { DataSource } from '../datasource';
import { DrillOptions, DrillQuery, DrillVariableQuery } from '../types';

type Props = QueryEditorProps<DataSource, DrillQuery, DrillOptions, DrillVariableQuery>;

export function VariableQueryEditor({ query, onChange }: Props) {
  const [sql, setSql] = useState(query.rawSql ?? '');
  return (
    <Stack direction="column" gap={1}>
      <Field
        label="SQL"
        description="One column gives the values. Name columns __text and __value to show a label different from the value."
      >
        <TextArea
          id="drill-variable-sql"
          rows={4}
          value={sql}
          placeholder="SELECT DISTINCT gender FROM cp.`employee.json`"
          onChange={(e) => setSql(e.currentTarget.value)}
          onBlur={() => onChange({ ...query, refId: query.refId || 'variable', rawSql: sql })}
        />
      </Field>
      <Text variant="bodySmall" color="secondary">
        Other variables and the time macros can be used in the SQL.
      </Text>
    </Stack>
  );
}

import React from 'react';
import { Stack, Text } from '@grafana/ui';

const macros: Array<[string, string]> = [
  ['$__timeFilter(col)', "col >= TIMESTAMP '<from>' AND col <= TIMESTAMP '<to>' (dashboard range, UTC)"],
  ['$__timeFrom(), $__timeTo()', 'The range bounds as TIMESTAMP literals'],
  ['$__unixEpochFilter(col)', 'Range predicate on a column of epoch seconds'],
  ['$__unixEpochFrom(), $__unixEpochTo()', 'The range bounds as epoch seconds'],
  ['$__timeGroup(col, 5m[, fill])', 'Buckets col into intervals; fill is NULL, previous or a number'],
  ['$__timeGroupAlias(col, $__interval)', 'Same, aliased AS `time`'],
  ['$__interval, $__interval_ms', 'The panel interval, for example 30s or 30000'],
];

export function MacroHelp() {
  return (
    <Stack direction="column" gap={0.5}>
      {macros.map(([macro, meaning]) => (
        <Text key={macro} variant="bodySmall">
          <code>{macro}</code> — {meaning}
        </Text>
      ))}
      <Text variant="bodySmall" color="secondary">
        Time series format needs a TIMESTAMP or DATE column; extra text columns split the result into series. Multi-value
        variables expand to quoted lists for IN (...).
      </Text>
    </Stack>
  );
}

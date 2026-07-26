/**
 * Unit tests for the paste-based import parsers in src/lib/util.ts.
 *
 * Zero dependencies: Node's built-in test runner, with Node's native TypeScript
 * type stripping handling the `.ts` import (Node >= 22.18 / 24). Run with
 * `npm run test:unit` from web/.
 *
 * The parsers are the whole trust boundary of the web importer — it never holds
 * a source credential, so everything it does is in these pure functions.
 */
import test from 'node:test'
import assert from 'node:assert/strict'

import {
  detectImportFormat,
  parseImport,
  parseEnvOrProps,
  IMPORT_SOURCES,
  importSource,
  type ImportedEntry,
} from '../../src/lib/util.ts'

/** Map a parse to a plain {key: value} for the happy-path assertions. */
function pairs(entries: ImportedEntry[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const e of entries) if (!e.error) out[e.key] = e.value
  return out
}

function errors(entries: ImportedEntry[]): ImportedEntry[] {
  return entries.filter(e => e.error)
}

/* ── detection ────────────────────────────────────────────── */

test('detectImportFormat recognises each supported shape', () => {
  const cases: [string, string][] = [
    ['', 'env'],
    ['   \n  ', 'env'],
    ['DATABASE_URL=postgres://x', 'env'],
    ['app.timeout: 30s', 'env'],
    ['# just a comment', 'env'],
    ['{ not json at all', 'env'],
    ['[1,2,3]', 'env'],
    ['"a bare json string"', 'env'],
    ['null', 'env'],
    ['{"API_KEY":"abc","PORT":"8080"}', 'doppler'],
    ['{"data":{"data":{"A":"1"},"metadata":{"version":2}}}', 'vault'],
    ['{"data":{"A":"1"}}', 'vault'],
    ['{"Name":"prod/db","SecretString":"{\\"U\\":\\"root\\"}"}', 'aws-sm'],
    ['{"SecretValues":[],"Errors":[]}', 'aws-sm'],
    ['{"Name":"prod/blob","SecretBinary":"YmFzZTY0"}', 'aws-sm'],
  ]
  for (const [text, want] of cases) {
    assert.equal(detectImportFormat(text), want, `detect(${JSON.stringify(text)})`)
  }
})

test('a Doppler blob with a string-valued "data" key is not mistaken for Vault', () => {
  assert.equal(detectImportFormat('{"data":"a string value","OTHER":"x"}'), 'doppler')
})

test('leading whitespace does not defeat detection', () => {
  assert.equal(detectImportFormat('\n\n  {"A":"1"}\n'), 'doppler')
})

/* ── the source catalogue ─────────────────────────────────── */

test('every format has a source card, and only .env lacks a command', () => {
  const ids = IMPORT_SOURCES.map(s => s.id)
  assert.deepEqual(ids, ['env', 'doppler', 'vault', 'aws-sm'])
  for (const s of IMPORT_SOURCES) {
    assert.ok(s.label.length > 0)
    assert.ok(s.short.length > 0)
    assert.ok(s.blurb.length > 0)
    if (s.id === 'env') assert.equal(s.command, null)
    else assert.ok(s.command && s.command.length > 0, `${s.id} needs a command`)
  }
  assert.equal(importSource('vault').id, 'vault')
})

test('the guidance commands are the documented read-only exports', () => {
  assert.match(importSource('doppler').command!, /^doppler secrets download --no-file --format json/)
  assert.match(importSource('vault').command!, /^vault kv get -format=json/)
  assert.match(importSource('aws-sm').command!, /^aws secretsmanager get-secret-value/)
  assert.match(importSource('aws-sm').altCommand!, /^aws secretsmanager batch-get-secret-value/)
})

/* ── empty / garbage input ────────────────────────────────── */

test('empty input parses to nothing, with no problem reported', () => {
  for (const text of ['', '   ', '\n\t\n']) {
    const r = parseImport(text)
    assert.deepEqual(r.entries, [])
    assert.equal(r.problem, undefined)
    assert.equal(r.detected, true)
  }
})

test('garbage falls through to the text parser rather than exploding', () => {
  // A line with no separator at all → one flagged entry, not a crash.
  const r = parseImport('%%% not anything %%%')
  assert.equal(r.format, 'env')
  assert.equal(errors(r.entries).length, 1)
  // A half-written JSON object is not JSON, so it is read as text, not thrown.
  const half = parseImport('{"A":"1",')
  assert.equal(half.format, 'env')
  assert.equal(half.problem, undefined)
})

test('an override that does not match the paste reports a problem, quoting nothing', () => {
  const r = parseImport('DATABASE_URL=postgres://example.invalid/app#CANARY-A', 'doppler')
  assert.equal(r.format, 'doppler')
  assert.equal(r.detected, false)
  assert.ok(r.problem)
  assert.ok(!r.problem!.includes('CANARY-A'), 'problem text must never echo the paste')
  assert.deepEqual(r.entries, [])
})

/* ── .env / .properties (unchanged behaviour) ─────────────── */

test('the existing .env/.properties parser still works through parseImport', () => {
  const text = [
    '# comment',
    'DATABASE_URL=postgres://localhost/app',
    'export API_TOKEN="tok_123"',
    'app.timeout: 30s',
    'BAD!KEY=x',
  ].join('\n')
  const direct = parseEnvOrProps(text)
  const r = parseImport(text)
  assert.equal(r.format, 'env')
  assert.equal(r.entries.length, direct.length)
  assert.deepEqual(pairs(r.entries), {
    DATABASE_URL: 'postgres://localhost/app',
    API_TOKEN: 'tok_123',
    'app.timeout': '30s',
  })
  assert.equal(errors(r.entries).length, 1)
})

test('duplicate keys are flagged but still listed', () => {
  const r = parseImport('A=1\nA=2')
  assert.equal(r.entries.length, 2)
  assert.equal(r.entries[0].note, undefined)
  assert.match(r.entries[1].note!, /duplicate key/)
})

/* ── Doppler ──────────────────────────────────────────────── */

test('Doppler: a flat JSON object imports verbatim', () => {
  const r = parseImport('{"DATABASE_URL":"postgres://db/app","API_KEY":"demo-api-key-1","EMPTY":""}')
  assert.equal(r.format, 'doppler')
  assert.equal(r.detected, true)
  assert.equal(r.problem, undefined)
  assert.deepEqual(pairs(r.entries), {
    DATABASE_URL: 'postgres://db/app',
    API_KEY: 'demo-api-key-1',
    EMPTY: '',
  })
  for (const e of r.entries) assert.equal(e.note, undefined, 'plain strings need no note')
})

test('Doppler: multi-line and escaped values survive the round trip', () => {
  const pem = '-----BEGIN KEY-----\nabc\n-----END KEY-----\n'
  const r = parseImport(JSON.stringify({ TLS_KEY: pem, QUOTED: 'a "b" c', TAB: 'a\tb' }))
  assert.equal(pairs(r.entries).TLS_KEY, pem)
  assert.equal(pairs(r.entries).QUOTED, 'a "b" c')
  assert.equal(pairs(r.entries).TAB, 'a\tb')
})

test('Doppler: an empty object yields no entries and no problem', () => {
  const r = parseImport('{}')
  assert.equal(r.format, 'doppler')
  assert.deepEqual(r.entries, [])
  assert.equal(r.problem, undefined)
})

test('Doppler: non-string values are JSON-encoded and say so', () => {
  const r = parseImport('{"PORT":8080,"DEBUG":true,"NOTHING":null,"OBJ":{"a":1},"ARR":[1,2]}')
  const byKey = Object.fromEntries(r.entries.map(e => [e.key, e]))
  assert.equal(byKey.PORT.value, '8080')
  assert.match(byKey.PORT.note!, /number → JSON-encoded/)
  assert.equal(byKey.DEBUG.value, 'true')
  assert.match(byKey.DEBUG.note!, /boolean → JSON-encoded/)
  assert.equal(byKey.NOTHING.value, 'null')
  assert.match(byKey.NOTHING.note!, /null → JSON-encoded/)
  assert.equal(byKey.OBJ.value, '{"a":1}')
  assert.match(byKey.OBJ.note!, /object → JSON-encoded/)
  assert.equal(byKey.ARR.value, '[1,2]')
  assert.match(byKey.ARR.note!, /array → JSON-encoded/)
  assert.equal(errors(r.entries).length, 0, 'encoded leaves are importable, not errors')
})

test('Doppler: a key that fails isValidKey is rejected per-key, not dropped', () => {
  const r = parseImport('{"GOOD_KEY":"1","bad key":"2","also/bad":"3","":"4","..":"5"}')
  assert.deepEqual(pairs(r.entries), { GOOD_KEY: '1' })
  const bad = errors(r.entries)
  assert.equal(bad.length, 4)
  for (const e of bad) {
    assert.match(e.error!, /invalid key/)
    assert.equal(e.value, '', 'a rejected key must not carry its value forward')
  }
})

test('Doppler: entry line numbers are unique so the preview can key on them', () => {
  const r = parseImport('{"A":"1","B":"2","C":"3"}')
  assert.deepEqual(r.entries.map(e => e.line), [1, 2, 3])
})

/* ── Vault KV v2 ──────────────────────────────────────────── */

const vaultFull = JSON.stringify({
  request_id: 'ec6b...',
  lease_id: '',
  data: {
    data: { DB_PASSWORD: 'REDACTED-EXAMPLE', DB_USER: 'app', REPLICAS: 3 },
    metadata: { created_time: '2026-07-01T00:00:00Z', version: 7 },
  },
})

test('Vault: the full `vault kv get -format=json` envelope unwraps data.data', () => {
  const r = parseImport(vaultFull)
  assert.equal(r.format, 'vault')
  assert.deepEqual(pairs(r.entries), { DB_PASSWORD: 'REDACTED-EXAMPLE', DB_USER: 'app', REPLICAS: '3' })
  assert.ok(!r.entries.some(e => e.key === 'metadata'), 'metadata must not become a secret')
  assert.match(r.entries.find(e => e.key === 'REPLICAS')!.note!, /number → JSON-encoded/)
})

test('Vault: a bare {"data":{…}} envelope is accepted', () => {
  const r = parseImport('{"data":{"A":"1","B":"2"}}')
  assert.equal(r.format, 'vault')
  assert.deepEqual(pairs(r.entries), { A: '1', B: '2' })
})

test('Vault: an already-unwrapped flat object works under an explicit override', () => {
  const r = parseImport('{"A":"1","B":"2"}', 'vault')
  assert.equal(r.format, 'vault')
  assert.equal(r.detected, false)
  assert.deepEqual(pairs(r.entries), { A: '1', B: '2' })
})

test('Vault: an empty data map reports a problem rather than a silent no-op', () => {
  const r = parseImport('{"data":{"data":{},"metadata":{"version":1}}}')
  assert.deepEqual(r.entries, [])
  assert.match(r.problem!, /empty data map/)
})

test('Vault: invalid keys inside data are rejected per-key', () => {
  const r = parseImport('{"data":{"data":{"OK":"1","no spaces please":"2"}}}')
  assert.deepEqual(pairs(r.entries), { OK: '1' })
  assert.equal(errors(r.entries).length, 1)
})

/* ── AWS Secrets Manager ──────────────────────────────────── */

test('AWS: get-secret-value with a JSON-object SecretString fans out one key per field', () => {
  const r = parseImport(JSON.stringify({
    ARN: 'arn:aws:secretsmanager:us-east-1:1:secret:prod/myapp/db-AbCdEf',
    Name: 'prod/myapp/db',
    SecretString: JSON.stringify({ USERNAME: 'appuser', PASSWORD: 'REDACTED-EXAMPLE', PORT: 5432 }),
    VersionId: 'v1',
  }))
  assert.equal(r.format, 'aws-sm')
  assert.deepEqual(pairs(r.entries), { USERNAME: 'appuser', PASSWORD: 'REDACTED-EXAMPLE', PORT: '5432' })
  assert.ok(!r.entries.some(e => e.key === 'ARN' || e.key === 'VersionId'), 'envelope fields are not secrets')
})

test('AWS: a plain-string SecretString becomes one key named after the trailing path segment', () => {
  const r = parseImport(JSON.stringify({ Name: 'prod/myapp/api-token', SecretString: 'tok_abc123' }))
  assert.deepEqual(pairs(r.entries), { 'api-token': 'tok_abc123' })
})

test('AWS: a plain-string SecretString with no Name is rejected with a reason', () => {
  const r = parseImport(JSON.stringify({ SecretString: 'tok_abc123' }))
  const bad = errors(r.entries)
  assert.equal(bad.length, 1)
  assert.match(bad[0].error!, /no Name/)
  assert.equal(bad[0].value, '')
})

test('AWS: a JSON array SecretString is not fanned out — only objects are', () => {
  const r = parseImport(JSON.stringify({ Name: 'prod/list', SecretString: '["a","b"]' }))
  assert.deepEqual(pairs(r.entries), { list: '["a","b"]' })
})

test('AWS: batch-get-secret-value merges every SecretValues element', () => {
  const r = parseImport(JSON.stringify({
    SecretValues: [
      { Name: 'prod/myapp/db', SecretString: JSON.stringify({ DB_USER: 'appuser', DB_PASS: 'p' }) },
      { Name: 'prod/myapp/api-token', SecretString: 'tok_1' },
    ],
    Errors: [],
  }))
  assert.equal(r.format, 'aws-sm')
  assert.deepEqual(pairs(r.entries), { DB_USER: 'appuser', DB_PASS: 'p', 'api-token': 'tok_1' })
  assert.equal(r.notice, undefined)
  assert.equal(new Set(r.entries.map(e => e.line)).size, r.entries.length, 'line ids must stay unique across the batch')
})

test('AWS: batch errors are reported as a non-blocking notice', () => {
  const r = parseImport(JSON.stringify({
    SecretValues: [{ Name: 'prod/a', SecretString: 'x' }],
    Errors: [{ SecretId: 'prod/denied', ErrorCode: 'AccessDeniedException' }],
  }))
  assert.deepEqual(pairs(r.entries), { a: 'x' })
  assert.match(r.notice!, /1 secret in this batch reported an error/)
  assert.equal(r.problem, undefined)
})

test('AWS: a binary secret is skipped with a clear reason, alone or in a batch', () => {
  const single = parseImport(JSON.stringify({ Name: 'prod/blob', SecretBinary: 'YmFzZTY0' }))
  assert.deepEqual(single.entries, [])
  assert.match(single.problem!, /Binary secret/)

  const batch = parseImport(JSON.stringify({
    SecretValues: [{ Name: 'prod/blob', SecretBinary: 'YmFzZTY0' }, { Name: 'prod/ok', SecretString: 'v' }],
  }))
  assert.deepEqual(pairs(batch.entries), { ok: 'v' })
  assert.match(errors(batch.entries)[0].error!, /binary secret/i)
})

test('AWS: a batch element with neither SecretString nor SecretBinary is an error row', () => {
  const r = parseImport(JSON.stringify({ SecretValues: [{ Name: 'prod/empty' }, 'not-an-object'] }))
  const bad = errors(r.entries)
  assert.equal(bad.length, 2)
  assert.match(bad[0].error!, /no SecretString/)
  assert.match(bad[1].error!, /not a secret object/)
})

test('AWS: output with neither SecretString nor SecretValues reports a problem', () => {
  const r = parseImport('{"ARN":"arn:...","Name":"prod/x"}', 'aws-sm')
  assert.deepEqual(r.entries, [])
  assert.match(r.problem!, /No `SecretString` or `SecretValues`/)
})

test('AWS: an empty batch reports a problem', () => {
  const r = parseImport('{"SecretValues":[]}')
  assert.deepEqual(r.entries, [])
  assert.match(r.problem!, /no secrets/)
})

test('AWS: invalid keys inside a fanned-out object are rejected per-key', () => {
  const r = parseImport(JSON.stringify({
    Name: 'prod/x',
    SecretString: JSON.stringify({ OK: '1', 'not ok': '2' }),
  }))
  assert.deepEqual(pairs(r.entries), { OK: '1' })
  assert.equal(errors(r.entries).length, 1)
})

/* ── no value ever leaks into an error or problem string ──── */

test('no parser puts a secret value into an error or problem message', () => {
  const canary = 'CANARY-VALUE-9f3a'
  const docs = [
    `KEY=${canary}`,
    `bad key=${canary}`,
    JSON.stringify({ 'bad key': canary }),
    JSON.stringify({ data: { data: { 'bad key': canary } } }),
    JSON.stringify({ SecretString: canary }),
    JSON.stringify({ SecretValues: [{ Name: 'x', SecretBinary: canary }] }),
    JSON.stringify({ Name: 'p/x', SecretString: JSON.stringify({ 'bad key': canary }) }),
  ]
  for (const d of docs) {
    for (const fmt of [null, 'env', 'doppler', 'vault', 'aws-sm'] as const) {
      const r = parseImport(d, fmt)
      assert.ok(!(r.problem ?? '').includes(canary), `problem leaked for ${fmt}`)
      assert.ok(!(r.notice ?? '').includes(canary), `notice leaked for ${fmt}`)
      for (const e of r.entries) {
        assert.ok(!(e.error ?? '').includes(canary), `entry error leaked for ${fmt}`)
        assert.ok(!(e.note ?? '').includes(canary), `entry note leaked for ${fmt}`)
        assert.ok(!(e.where ?? '').includes(canary), `entry location leaked for ${fmt}`)
      }
    }
  }
})

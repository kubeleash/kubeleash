// SPDX-License-Identifier: Apache-2.0
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

import { expandTilde, policyPathFromArgs, ensurePolicy } from './launch.mjs';

test('policyPathFromArgs extracts --policy value', () => {
  assert.equal(policyPathFromArgs(['--policy', '/p']), '/p');
  assert.equal(policyPathFromArgs(['--policy=/p']), '/p');
  assert.equal(policyPathFromArgs(['a', '--policy', '/q', 'b']), '/q');
  assert.equal(policyPathFromArgs(['--foo', 'bar']), null);
  assert.equal(policyPathFromArgs(['--policy']), null); // flag with no value
  // empty value means "no path" — treated as an empty string, not null
  assert.equal(policyPathFromArgs(['--policy=']), '');
});

test('expandTilde expands a leading ~ only', () => {
  const home = os.homedir();
  assert.equal(expandTilde('~/x'), path.join(home, 'x'));
  assert.equal(expandTilde('~'), home);
  assert.equal(expandTilde('/abs'), '/abs');
  assert.equal(expandTilde('rel/x'), 'rel/x');
  assert.equal(expandTilde(''), '');
  assert.equal(expandTilde('~bob/x'), '~bob/x'); // ~user unsupported
});

test('ensurePolicy: skipped on null, creates when missing, never overwrites', async (t) => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'kl-policy-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));

  assert.equal(await ensurePolicy(null), 'skipped');

  const p = path.join(dir, 'nested', 'policy.yaml');
  assert.equal(await ensurePolicy(p), 'created');
  const written = await fs.readFile(p, 'utf8');
  assert.match(written, /verbs: \[get, list, watch\]/);
  assert.match(written, /deny:/);

  await fs.writeFile(p, 'SENTINEL', { flag: 'w' });
  assert.equal(await ensurePolicy(p), 'exists');
  assert.equal(await fs.readFile(p, 'utf8'), 'SENTINEL'); // not overwritten
});

test('ensurePolicy expands a leading ~ before writing', async (t) => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'kl-home-'));
  t.after(() => fs.rm(home, { recursive: true, force: true }));
  const orig = os.homedir;
  os.homedir = () => home;            // expandTilde reads os.homedir()
  t.after(() => { os.homedir = orig; });

  const status = await ensurePolicy('~/.kubeleash/policy.yaml');
  assert.equal(status, 'created');
  const written = await fs.readFile(path.join(home, '.kubeleash/policy.yaml'), 'utf8');
  assert.match(written, /verbs: \[get, list, watch\]/);
});

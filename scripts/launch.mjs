#!/usr/bin/env node
// kubeleash plugin launcher.
//
// The Claude Code plugin points its MCP server `command` at this script so a
// plugin install also gets the binary. Resolution order:
//   1. `kubeleash` already on PATH (respects `brew install` / `go install`)
//   2. a previously cached managed binary for this plugin's version
//   3. download the matching binary from the GitHub release, verify its
//      SHA-256 against the release `checksums.txt`, cache it, then run it
//
// All argv after this script (e.g. `--policy <path>`) and the inherited env
// (e.g. KUBECONFIG) are forwarded to the binary unchanged, with stdio passed
// straight through so the MCP stdio transport is transparent.
//
// Env overrides: KUBELEASH_BIN (use this exact binary), KUBELEASH_FORCE_DOWNLOAD=1
// (ignore PATH), KUBELEASH_CACHE_DIR (where to cache the managed binary).

import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createWriteStream } from 'node:fs';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO = 'kubeleash/kubeleash';

function die(msg) {
  // Diagnostics go to stderr — stdout is the MCP transport.
  process.stderr.write(`kubeleash launcher: ${msg}\n`);
  process.exit(1);
}

function pluginRoot() {
  if (process.env.CLAUDE_PLUGIN_ROOT) return process.env.CLAUDE_PLUGIN_ROOT;
  // <root>/scripts/launch.mjs -> <root>
  return path.dirname(path.dirname(fileURLToPath(import.meta.url)));
}

async function pluginVersion() {
  try {
    const raw = await fs.readFile(path.join(pluginRoot(), '.claude-plugin', 'plugin.json'), 'utf8');
    const v = JSON.parse(raw).version;
    if (typeof v === 'string' && v.length) return v;
  } catch { /* fall through */ }
  die('could not read version from .claude-plugin/plugin.json');
}

async function isExecutable(p) {
  try { await fs.access(p, fs.constants.X_OK); return true; } catch { return false; }
}

async function findOnPath(name) {
  const exts = process.platform === 'win32' ? (process.env.PATHEXT || '.EXE').split(';') : [''];
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    if (!dir) continue;
    for (const ext of exts) {
      const candidate = path.join(dir, name + ext.toLowerCase());
      if (await isExecutable(candidate)) return candidate;
      const candidateUpper = path.join(dir, name + ext);
      if (await isExecutable(candidateUpper)) return candidateUpper;
    }
  }
  return null;
}

function assetFor(version) {
  const bare = version.replace(/^v/, '');
  const goos = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[process.platform];
  const goarch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
  if (!goos || !goarch) die(`unsupported platform ${process.platform}/${process.arch}`);
  const isWin = goos === 'windows';
  return {
    archive: `kubeleash_${bare}_${goos}_${goarch}.${isWin ? 'zip' : 'tar.gz'}`,
    binName: isWin ? 'kubeleash.exe' : 'kubeleash',
  };
}

function cacheDir(version) {
  if (process.env.KUBELEASH_CACHE_DIR) return path.join(process.env.KUBELEASH_CACHE_DIR, `v${version.replace(/^v/, '')}`);
  const base = process.platform === 'win32'
    ? path.join(process.env.LOCALAPPDATA || path.join(os.homedir(), 'AppData', 'Local'), 'kubeleash', 'cache')
    : process.env.XDG_CACHE_HOME
      ? path.join(process.env.XDG_CACHE_HOME, 'kubeleash')
      : path.join(os.homedir(), '.cache', 'kubeleash');
  return path.join(base, `v${version.replace(/^v/, '')}`);
}

async function fetchToFile(url, dest) {
  const res = await fetch(url, { redirect: 'follow' });
  if (!res.ok) die(`download failed (${res.status}) for ${url}`);
  await pipeline(Readable.fromWeb(res.body), createWriteStream(dest));
}

async function sha256(file) {
  const h = createHash('sha256');
  await pipeline((await fs.open(file)).createReadStream(), h);
  return h.digest('hex');
}

async function verifyChecksum(tag, archivePath, archiveName) {
  const url = `https://github.com/${REPO}/releases/download/${tag}/checksums.txt`;
  const res = await fetch(url, { redirect: 'follow' });
  if (!res.ok) die(`could not fetch checksums.txt (${res.status})`);
  const text = await res.text();
  const line = text.split('\n').find((l) => l.trim().endsWith(`  ${archiveName}`) || l.trim().endsWith(` ${archiveName}`));
  if (!line) die(`no checksum entry for ${archiveName}`);
  const expected = line.trim().split(/\s+/)[0].toLowerCase();
  const actual = (await sha256(archivePath)).toLowerCase();
  if (expected !== actual) die(`checksum mismatch for ${archiveName}\n  expected ${expected}\n  actual   ${actual}`);
}

async function extract(archivePath, intoDir) {
  // bsdtar (macOS/Linux `tar`, and Windows' bundled `tar`) extracts both
  // .tar.gz and .zip, so one command covers every platform.
  await new Promise((resolve, reject) => {
    const p = spawn('tar', ['-xf', archivePath, '-C', intoDir], { stdio: ['ignore', 'ignore', 'inherit'] });
    p.on('error', reject);
    p.on('close', (code) => (code === 0 ? resolve() : reject(new Error(`tar exited ${code}`))));
  });
}

async function ensureBinary() {
  if (process.env.KUBELEASH_BIN) return process.env.KUBELEASH_BIN;

  if (process.env.KUBELEASH_FORCE_DOWNLOAD !== '1') {
    const onPath = await findOnPath('kubeleash');
    if (onPath) return onPath;
  }

  const version = await pluginVersion();
  const tag = `v${version.replace(/^v/, '')}`;
  const { archive, binName } = assetFor(version);
  const dir = cacheDir(version);
  const binPath = path.join(dir, binName);
  if (await isExecutable(binPath)) return binPath;

  await fs.mkdir(dir, { recursive: true });
  const tmp = await fs.mkdtemp(path.join(os.tmpdir(), 'kubeleash-'));
  try {
    const archivePath = path.join(tmp, archive);
    process.stderr.write(`kubeleash launcher: fetching ${tag} for ${process.platform}/${process.arch}…\n`);
    await fetchToFile(`https://github.com/${REPO}/releases/download/${tag}/${archive}`, archivePath);
    await verifyChecksum(tag, archivePath, archive);
    await extract(archivePath, tmp);
    await fs.chmod(path.join(tmp, binName), 0o755).catch(() => {});
    // Atomic-ish install: move the extracted binary into the cache.
    await fs.rename(path.join(tmp, binName), binPath).catch(async () => {
      await fs.copyFile(path.join(tmp, binName), binPath);
      await fs.chmod(binPath, 0o755).catch(() => {});
    });
    return binPath;
  } finally {
    await fs.rm(tmp, { recursive: true, force: true }).catch(() => {});
  }
}

async function main() {
  const bin = await ensureBinary();
  const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit', env: process.env });
  child.on('error', (e) => die(`failed to start ${bin}: ${e.message}`));
  for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    process.on(sig, () => { try { child.kill(sig); } catch { /* ignore */ } });
  }
  child.on('close', (code, signal) => {
    if (signal) process.kill(process.pid, signal);
    else process.exit(code ?? 0);
  });
}

main().catch((e) => die(e?.stack || String(e)));

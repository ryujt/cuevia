import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const selector = fileURLToPath(new URL('../scripts/select-signing-identity.awk', import.meta.url));
const development = 'Apple Development: Local Developer (DEVELOPER)';
const distribution = 'Developer ID Application: Release Team (RELEASE)';
const devHash = 'A'.repeat(40);
const releaseHash = 'B'.repeat(40);
const identity = (hash, name) => `  1) ${hash} "${name}"\n`;
const both = identity(releaseHash, distribution) + identity(devHash, development);

function select(input, installed = '', built = '') {
  const result = spawnSync('/usr/bin/awk', [
    '-v', `installed_authority=${installed}`, '-v', `built_authority=${built}`, '-f', selector,
  ], { input, encoding: 'utf8' });
  assert.ifError(result.error);
  return result;
}

test('one valid certificate is selected', () => {
  const result = select(identity(releaseHash, distribution));
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), releaseHash);
});

test('installed signer wins over the last release build and keychain order', () => {
  const result = select(both, development, distribution);
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), devHash);
});

test('an installed Developer ID signer is preserved', () => {
  const result = select(both, distribution, development);
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), releaseHash);
});

test('the previous build signer is reused when no installation is present', () => {
  const result = select(both, '', distribution);
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), releaseHash);
});

test('a fresh local build prefers a unique Apple Development identity', () => {
  const result = select(both);
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), devHash);
});

test('unresolved certificates require an explicit selection', () => {
  const result = select(both + identity('C'.repeat(40), 'Apple Development: Another Developer (OTHER)'));
  assert.equal(result.status, 1);
  assert.equal(result.stdout, '');
  assert.match(result.stderr, /CUEVIA_SIGN_IDENTITY/);
});

test('duplicate authority names do not silently change the installed signer', () => {
  const result = select(both + identity('C'.repeat(40), development), development, distribution);
  assert.equal(result.status, 1);
  assert.equal(result.stdout, '');
  assert.match(result.stderr, /Multiple signing identities/);
});

test('duplicate fingerprints and unrelated certificates are ignored', () => {
  const result = select(identity(devHash, development).repeat(2)
    + identity('C'.repeat(40), 'Developer ID Installer: Release Team (RELEASE)')
    + identity('not-a-fingerprint', distribution));
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), devHash);
});

test('missing signing certificates fail before building', () => {
  const result = select('     0 valid identities found\n');
  assert.equal(result.status, 1);
  assert.equal(result.stdout, '');
  assert.match(result.stderr, /No valid/);
});

#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

node --check skills/pi-vendor/scripts/vendor.mjs
node --check skills/pi-vendor/scripts/vendor-shared.js
node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
const shared = await import('./skills/pi-vendor/scripts/vendor-shared.js');
for (const name of ['candidateCatalogPaths', 'encodeConfigLiteral', 'isPlainDataTree', 'stripRoutingFields']) {
  assert.equal(typeof shared[name], 'function', `missing vendor shared export ${name}`);
}
console.log('pi-vendor skill checks passed');
NODE

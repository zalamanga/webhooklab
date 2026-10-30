import { createHash } from 'node:crypto';
import { readdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

const hashes = new Set();
async function inspect(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const name = path.join(directory, entry.name);
    if (entry.isDirectory()) await inspect(name);
    else if (entry.name.endsWith('.html')) {
      const html = await readFile(name, 'utf8');
      for (const match of html.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/gi)) {
        if (!/\bsrc\s*=/i.test(match[1]) && match[2]) {
          hashes.add(`'sha256-${createHash('sha256').update(match[2], 'utf8').digest('base64')}'`);
        }
      }
    }
  }
}
await inspect('out');
if (hashes.size === 0) throw new Error('Next.js export has no inline bootstrap scripts to hash.');
const policy = [
  "default-src 'self'", "base-uri 'none'", "object-src 'none'", "frame-ancestors 'none'",
  "form-action 'self'", "connect-src 'self'", "img-src 'self' data:", "font-src 'self'",
  "style-src 'self' 'unsafe-inline'", `script-src 'self' ${[...hashes].sort().join(' ')}`,
].join('; ');
await writeFile('generated-headers.conf', [
  `add_header Content-Security-Policy "${policy}" always;`,
  'add_header X-Content-Type-Options "nosniff" always;',
  'add_header Referrer-Policy "no-referrer" always;',
  'add_header X-Frame-Options "DENY" always;',
  'add_header Permissions-Policy "camera=(), microphone=(), geolocation=()" always;',
  '',
].join('\n'));
console.log(`Generated CSP for ${hashes.size} distinct inline bootstrap scripts.`);

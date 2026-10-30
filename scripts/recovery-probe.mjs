import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import http from 'node:http';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const root = path.resolve(import.meta.dirname, '..');
const baseURL = process.env.WEBHOOK_BASE_URL || 'http://127.0.0.1:8097';
const password = process.env.WEBHOOK_DEMO_PASSWORD;
if (!password) throw new Error('Set WEBHOOK_DEMO_PASSWORD, or run scripts/Recovery-Probe.ps1.');
if (baseURL !== 'http://127.0.0.1:8097') throw new Error('Recovery probe only operates this local Docker workspace on port8097.');
let cookie = '';
let csrf = '';
async function api(route, options = {}) {
  const response = await fetch(baseURL + '/api' + route, {
    ...options, signal: AbortSignal.timeout(8000),
    headers: { Origin: baseURL, 'Content-Type': 'application/json', Cookie: cookie, 'X-CSRF-Token': csrf, ...options.headers },
  });
  if (!response.ok) {
    const error = await response.json().catch(() => ({}));
    throw new Error(`Probe request ${route} failed: HTTP${response.status}, ${error.code || 'unknown error'}`);
  }
  if (route === '/auth/login') cookie = response.headers.getSetCookie().find(value => value.startsWith('webhooklab_session=')).split(';', 1)[0];
  return response.status === 204 ? null : response.json();
}
function docker(args) {
  const child = spawnSync('docker', ['compose', '-p', 'webhooklab', ...args], { cwd: root, encoding: 'utf8', timeout: 45000, windowsHide: true });
  if (child.error || child.status !== 0) throw new Error(`Owned Docker operation failed: ${args.join(' ')}`);
  return child.stdout.trim();
}
const contextQuery = spawnSync('docker', ['context', 'inspect'], { cwd: root, encoding: 'utf8', timeout: 45000, windowsHide: true });
if (contextQuery.error || contextQuery.status !== 0) throw new Error('Cannot inspect the selected local Docker context.');
const engineHost = process.env.DOCKER_HOST || JSON.parse(contextQuery.stdout)[0].Endpoints.docker.Host;
let socketPath;
if (engineHost.startsWith('unix://')) socketPath = engineHost.slice(7);
else if (engineHost.startsWith('npipe://')) socketPath = engineHost.slice(8).replaceAll('/', '\\');
else throw new Error('Recovery probe requires a local Unix socket or Windows named-pipe Docker context.');
const engineAgent = new http.Agent({ keepAlive: true });
async function engine(route, method = 'GET') {
  return new Promise((resolve, reject) => {
    const request = http.request({ socketPath, path: route, method, agent: engineAgent, headers: { Host: 'docker', 'Content-Length': '0' } }, response => {
      let body = '';
      response.setEncoding('utf8');
      response.on('data', chunk => { body += chunk; if (body.length > 65536) request.destroy(new Error('Unexpectedly large Docker metadata response.')); });
      response.on('end', () => {
        if (response.statusCode < 200 || response.statusCode > 299) { reject(new Error(`Local Docker API returned HTTP${response.statusCode}`)); return; }
        try { resolve(body ? JSON.parse(body) : null); } catch { reject(new Error('Invalid Docker metadata response.')); }
      });
    });
    request.setTimeout(8000, () => request.destroy(new Error('Local Docker API timed out.')));
    request.on('error', reject);
    request.end();
  });
}
const engineVersion = (await engine('/version')).ApiVersion;
const filter = encodeURIComponent(JSON.stringify({ label: ['com.docker.compose.project=webhooklab', 'com.docker.compose.service=worker'] }));
const workers = await engine(`/v${engineVersion}/containers/json?filters=${filter}`);
if (workers.length !== 1) throw new Error('Probe requires exactly one running worker in this WebhookLab project.');
const worker = workers[0];
const owner = worker.Labels['com.docker.compose.project.working_dir'];
if (!owner || path.resolve(owner).toLowerCase() !== root.toLowerCase()) throw new Error('Worker belongs to a different checkout.');
async function queue(paused) {
  const current = (await api('/dashboard')).queue;
  if (current.paused === paused) return;
  await api('/queue', { method: 'PUT', body: JSON.stringify({ expectedVersion: current.version, paused }) });
}
async function mode(value) {
  const destination = (await api('/destinations')).find(item => item.id === 'warehouse');
  if (destination.mode === value) return;
  await api('/destinations/warehouse', { method: 'PUT', body: JSON.stringify({ expectedVersion: destination.version, mode: value }) });
}
async function waitFor(read, predicate, limitMs, description) {
  const deadline = Date.now() + limitMs;
  while (Date.now() < deadline) {
    const result = await read();
    if (predicate(result)) return result;
    await new Promise(resolve => setTimeout(resolve, 150));
  }
  throw new Error('Probe timed out: ' + description);
}
const session = await api('/auth/login', { method: 'POST', body: JSON.stringify({ email: 'operator@webhooklab.test', password }) });
csrf = session.csrfToken;
const originalQueue = (await api('/dashboard')).queue.paused;
const originalMode = (await api('/destinations')).find(item => item.id === 'warehouse').mode;
let workerKilled = false;
let event;
try {
  await queue(true);
  await mode('TIMEOUT');
  event = await api('/events', {
    method: 'POST', headers: { 'Idempotency-Key': randomUUID() },
    body: JSON.stringify({ destinationId: 'warehouse', eventType: 'order.created', payload: JSON.stringify({ reference: 'DEMO-RECOVERY', scenario: 'receiver commits then sender worker is killed', synthetic: true }) }),
  });
  const detail = () => api('/events/' + event.id);
  await queue(false);
  await waitFor(detail, result => result.delivery.state === 'SENDING' && result.receipt?.effectCount === 1, 10000, 'receiver effect committed while attempt is SENDING');
  workerKilled = true;
  await engine(`/v${engineVersion}/containers/${worker.Id}/kill?signal=SIGKILL`, 'POST');
  await queue(true);
  const interrupted = await detail();
  assert.equal(interrupted.delivery.state, 'SENDING', 'Worker must be killed before it records the timeout result.');
  assert.equal(interrupted.attempts[0].status, 'STARTED');
  assert.equal(interrupted.receipt.effectCount, 1);
  await mode('HEALTHY');
  docker(['up', '-d', '--no-build', 'worker']);
  workerKilled = false;
  const recovered = await waitFor(detail, result => result.attempts.some(item => item.status === 'ABANDONED'), 25000, 'expired lease recovered while queue paused');
  assert.equal(recovered.delivery.state, 'RETRY_WAIT');
  await queue(false);
  const delivered = await waitFor(detail, result => result.delivery.state === 'DELIVERED', 15000, 'recovered delivery succeeds');
  assert.equal(delivered.receipt.effectCount, 1);
  assert.equal(delivered.receipt.receiptCount, 2);
  assert.equal(delivered.delivery.totalAttempts, 2);
  assert(delivered.attempts.some(item => item.status === 'ABANDONED'));
  assert(delivered.attempts.some(item => item.status === 'SUCCEEDED'));
  assert.match(event.id, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i);
  const databaseEffectRows = Number(docker(['exec', '-T', 'db', 'psql', '-U', 'webhooklab', '-d', 'webhooklab', '-tAc',
    `SELECT count(*) FROM receiver_effects WHERE destination_id='warehouse' AND event_id='${event.id}'::uuid;`]));
  assert.equal(databaseEffectRows, 1, 'Check the actual synthetic-effect row, separately from the API receipt projection.');
  const report = {
    checkedAt: new Date().toISOString(), eventId: event.id,
    workerKilledAfterReceiverEffect: true, leaseRecoveredWhilePaused: true,
    finalState: delivered.delivery.state, attemptStatuses: delivered.attempts.map(item => item.status),
    totalAttempts: delivered.delivery.totalAttempts, receiptCount: delivered.receipt.receiptCount,
    effectCount: delivered.receipt.effectCount, databaseEffectRows,
  };
  await mkdir(path.join(root, '.local'), { recursive: true });
  await writeFile(path.join(root, '.local/recovery-probe.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
} finally {
  try { if (workerKilled) docker(['up', '-d', '--no-build', 'worker']); }
  finally {
    try { await mode(originalMode); }
    finally { try { await queue(originalQueue); } finally { engineAgent.destroy(); } }
  }
}

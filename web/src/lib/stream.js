// SSE client: one EventSource feeding Svelte stores. Reconnects with backoff.
import { writable } from 'svelte/store'

export const containers = writable([])
export const stats = writable({})   // name -> ContainerStats
export const host = writable(null)
export const mc = writable(null)
export const wan = writable(null)
export const connected = writable(false)

// ring buffers for live charts: { seriesKey: {t:[], v:[]} }
export const history = writable({})
const MAX_POINTS = 720 // ~1h at 5s

function pushPoint(key, time, value) {
  history.update((h) => {
    const s = h[key] ?? { t: [], v: [] }
    s.t.push(Math.floor(new Date(time).getTime() / 1000))
    s.v.push(value)
    if (s.t.length > MAX_POINTS) { s.t.shift(); s.v.shift() }
    return { ...h, [key]: s }
  })
}

let es = null
let retryMs = 1000

export function connect() {
  if (es) es.close()
  es = new EventSource('/api/stream/stats')

  es.onopen = () => { connected.set(true); retryMs = 1000 }
  es.onerror = () => {
    connected.set(false)
    es.close()
    setTimeout(connect, retryMs)
    retryMs = Math.min(retryMs * 2, 30000)
  }

  es.addEventListener('snapshot', (e) => {
    const snap = JSON.parse(e.data)
    containers.set(snap.containers ?? [])
    stats.set(snap.stats ?? {})
    if (snap.host?.time) host.set(snap.host)
    if (snap.mc?.time) mc.set(snap.mc)
    if (snap.wan?.time) wan.set(snap.wan)
  })

  es.addEventListener('container', (e) => {
    const st = JSON.parse(e.data)
    stats.update((s) => ({ ...s, [st.name]: st }))
    pushPoint(`container.${st.name}.cpu`, st.time, st.cpuPercent)
    pushPoint(`container.${st.name}.mem`, st.time, st.memUsage / 1024 / 1024 / 1024)
  })

  es.addEventListener('host', (e) => {
    const h = JSON.parse(e.data)
    host.set(h)
    pushPoint('host.cpu', h.time, h.cpuPercent)
    pushPoint('host.mem', h.time, h.memUsed / 1024 / 1024 / 1024)
  })

  es.addEventListener('mc', (e) => {
    const m = JSON.parse(e.data)
    mc.set(m)
    pushPoint('mc.tps', m.time, m.tps)
    pushPoint('mc.players', m.time, m.playersOnline)
  })

  es.addEventListener('wan', (e) => {
    const w = JSON.parse(e.data)
    wan.set(w)
    for (const t of w.targets ?? []) {
      pushPoint(`wan.${t.target}.rtt`, w.time, t.reached ? t.rttMs : null)
    }
  })
}

async function apiFetch(path, opts = {}) {
  const resp = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  })
  if (resp.status === 401 && path !== '/api/login') {
    authenticated.set(false)
    throw new Error('nicht angemeldet')
  }
  const data = await resp.json().catch(() => ({}))
  if (!resp.ok) throw new Error(data.error ?? resp.statusText)
  return data
}

export async function sendRcon(command) {
  const data = await apiFetch('/api/rcon', {
    method: 'POST',
    body: JSON.stringify({ command }),
  })
  return data.response
}

// --- auth ---
export const authRequired = writable(false)
export const authenticated = writable(true)

export async function checkAuth() {
  const data = await apiFetch('/api/auth')
  authRequired.set(data.required)
  authenticated.set(data.authenticated)
  return data.authenticated
}

export async function login(password) {
  await apiFetch('/api/login', { method: 'POST', body: JSON.stringify({ password }) })
  authenticated.set(true)
}

export async function logout() {
  await apiFetch('/api/logout', { method: 'POST' })
  authenticated.set(false)
}

// --- container actions ---
export function containerAction(name, action) {
  return apiFetch(`/api/containers/${encodeURIComponent(name)}/${action}`, { method: 'POST' })
}

// --- routines ---
export const listRoutines = () => apiFetch('/api/routines')
export const createRoutine = (r) => apiFetch('/api/routines', { method: 'POST', body: JSON.stringify(r) })
export const updateRoutine = (r) => apiFetch(`/api/routines/${r.id}`, { method: 'PUT', body: JSON.stringify(r) })
export const deleteRoutine = (id) => apiFetch(`/api/routines/${id}`, { method: 'DELETE' })
export const runRoutine = (id) => apiFetch(`/api/routines/${id}/run`, { method: 'POST' })
export const recentRuns = () => apiFetch('/api/routine-runs')
export const auditLog = () => apiFetch('/api/audit')

// --- mods (Phase 3) ---
export const listMods = (profile) => apiFetch(`/api/mods?profile=${encodeURIComponent(profile)}`)
export const checkMods = (profile) => apiFetch('/api/mods/check', { method: 'POST', body: JSON.stringify({ profile }) })
export const stageMods = (profile, filenames) =>
  apiFetch('/api/mods/stage', { method: 'POST', body: JSON.stringify({ profile, filenames }) })
export const applyMods = (profile, restart) =>
  apiFetch('/api/mods/apply', { method: 'POST', body: JSON.stringify({ profile, restart }) })
export const rollbackMods = (profile) =>
  apiFetch('/api/mods/rollback', { method: 'POST', body: JSON.stringify({ profile }) })
export const versionWatch = () => apiFetch('/api/version-watch')
export const versionWatchCheck = () => apiFetch('/api/version-watch/check', { method: 'POST' })

// --- backup/restore (Phase 4.4) ---
export const restorePlayer = (uuid) =>
  apiFetch('/api/backup/restore-player', { method: 'POST', body: JSON.stringify({ uuid }) })

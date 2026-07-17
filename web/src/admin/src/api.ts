// Client for the backend's /admin/api/* endpoints (server/internal/
// adminconsole). Auth is a SEPARATE session cookie from the regular user
// system (server/internal/adminauth, cookie "admin_session"): every call
// sends credentials: 'include', and the backend's CORS layer echoes the
// request Origin for /admin/* (cmd/server/main.go withAdminCORS) so a
// credentialed cross-origin response is accepted. A 401 means the admin
// session is missing/expired; callers drop back to the login screen.

export interface AdminUser {
  email: string
}

// One row of the user table. Mirrors model.AdminUserSummary on the backend.
// Plan/quota/usage are intentionally out of scope for this admin console —
// the backend only exposes basic identity fields.
export interface UserSummary {
  id: string
  email: string
  name: string
  avatarColor: string
}

export interface UsersResponse {
  total: number
  users: UserSummary[]
}

// Same resolution strategy as the main web app's api.ts BASE: an explicit
// VITE_ADMIN_API_URL for local dev against a separately-running backend,
// falling back to the serving origin (correct in production, where the
// admin SPA is embedded same-origin under /admin).
export const BASE: string = import.meta.env.VITE_ADMIN_API_URL ?? window.location.origin

export class ApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request(method: string, path: string, body?: unknown): Promise<Response> {
  let res: Response
  try {
    res = await fetch(`${BASE}${path}`, {
      method,
      credentials: 'include',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  } catch {
    throw new ApiError(0, `Cannot reach the backend at ${BASE}. Is it running?`)
  }
  if (!res.ok) {
    const text = (await res.text()).trim()
    throw new ApiError(res.status, text || res.statusText)
  }
  return res
}

export const api = {
  login: (email: string, password: string): Promise<AdminUser> =>
    request('POST', '/admin/api/login', { email, password }).then((r) => r.json()),

  logout: (): Promise<void> => request('POST', '/admin/api/logout').then(() => undefined),

  me: (): Promise<AdminUser> => request('GET', '/admin/api/me').then((r) => r.json()),

  listUsers: (): Promise<UsersResponse> => request('GET', '/admin/api/users').then((r) => r.json()),
}

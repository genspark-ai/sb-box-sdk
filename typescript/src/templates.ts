import { TemplateBuildError, WaitTimeoutError } from './errors.js'
import type { Http } from './http.js'
import type { Grant, GrantOptions, Template, TemplateBuild, TemplateBuildOptions, WaitOptions } from './types.js'

export class Templates {
  constructor(private readonly http: Http) {}

  /** Start a build. Async: poll `get()` or use `waitReady()`. */
  async build(opts: TemplateBuildOptions): Promise<TemplateBuild> {
    if (!opts.image && !opts.dockerfile) throw new TypeError('templates.build: image or dockerfile is required')
    const body: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(opts)) if (v !== undefined) body[k] = v
    return this.http.request<TemplateBuild>('/templates/build', { method: 'POST', json: body, timeout: 120 })
  }

  /** Poll until the template is `ready`; throws `TemplateBuildError` on `error`. */
  async waitReady(templateId: string, opts: WaitOptions = {}): Promise<Template> {
    const timeout = (opts.timeout ?? 600) * 1000
    const interval = (opts.interval ?? 5) * 1000
    const deadline = Date.now() + timeout
    while (true) {
      const t = await this.get(templateId)
      if (t.status === 'ready') return t
      if (t.status === 'error') throw new TemplateBuildError(templateId, t.buildError, t)
      if (Date.now() + interval > deadline) {
        throw new WaitTimeoutError(`template ${templateId} still ${t.status} after ${timeout / 1000}s`)
      }
      await new Promise((r) => setTimeout(r, interval))
    }
  }

  async list(): Promise<Template[]> {
    const r = await this.http.request<{ templates: Template[] }>('/templates', { retry: true })
    return r.templates ?? []
  }

  async shared(): Promise<Template[]> {
    return this.http.request<Template[]>('/templates/shared', { retry: true })
  }

  async get(templateId: string): Promise<Template> {
    return this.http.request<Template>(`/templates/${encodeURIComponent(templateId)}`, { retry: true })
  }

  async delete(templateId: string): Promise<void> {
    await this.http.request(`/templates/${encodeURIComponent(templateId)}`, { method: 'DELETE' })
  }

  async grant(templateId: string, userId: string, opts: GrantOptions = {}): Promise<Grant> {
    const json: Record<string, unknown> = { perm: opts.perm ?? 'read' }
    if (opts.expiresAt) json.expires_at = opts.expiresAt
    if (opts.reason) json.reason = opts.reason
    return this.http.request<Grant>(
      `/templates/${encodeURIComponent(templateId)}/grants/${encodeURIComponent(userId)}`,
      { method: 'PUT', json },
    )
  }

  async grants(templateId: string): Promise<Grant[]> {
    return this.http.request<Grant[]>(`/templates/${encodeURIComponent(templateId)}/grants`, { retry: true })
  }

  async revoke(templateId: string, userId: string): Promise<void> {
    await this.http.request(
      `/templates/${encodeURIComponent(templateId)}/grants/${encodeURIComponent(userId)}`,
      { method: 'DELETE' },
    )
  }
}

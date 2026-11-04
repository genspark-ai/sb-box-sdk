export type SandboxState =
  | 'starting' | 'running' | 'freezing' | 'frozen' | 'exited' | 'failed' | 'unknown'

export type ListState = SandboxState | 'all'

export interface Regions {
  regions: string[]
  default: string
}

export interface Limits {
  plan: string
  planRequired?: boolean
  limits: {
    maxConcurrentSandboxes: number
    maxCpu: number
    maxMemoryMB: number
    maxDiskGB: number
    maxConcurrentBuilds: number
    [k: string]: unknown
  }
  usage: { runningSandboxes: number; buildingTemplates: number; [k: string]: unknown }
  [k: string]: unknown
}

export interface UsageTotals {
  runSec?: number
  cpuCoreSec?: number
  memGBSec?: number
  diskGBSec?: number
  credits?: number
  [k: string]: unknown
}

export interface Usage {
  days: number
  from: string
  to: string
  rateCard?: Record<string, unknown>
  direct?: UsageTotals
  daily?: Array<{ day: string; direct?: Record<string, unknown>; [k: string]: unknown }>
  [k: string]: unknown
}

export type TemplateStatus = 'building' | 'ready' | 'error'

export interface Template {
  id: string
  name: string
  description?: string
  namespace: 'public' | 'private'
  base_image?: string
  status: TemplateStatus
  buildError?: string
  region?: string
  is_owner?: boolean
  can_create?: boolean
  cogen_id?: string
  created_at?: string
  updated_at?: string
  cpuCount?: number
  memoryMB?: number
  diskSizeGB?: number
  startCmd?: string
  readyCmd?: string
  [k: string]: unknown
}

export interface TemplateBuildOptions {
  name?: string
  image?: string
  dockerfile?: string
  cpuCount?: number
  memoryMB?: number
  diskSizeGB?: number
  startCmd?: string
  readyCmd?: string
  region?: string
  logPaths?: string[]
  description?: string
}

export interface TemplateBuild {
  templateId: string
  status: string
  region?: string
  owner?: string
  [k: string]: unknown
}

export type Permission = 'read' | 'write'

export interface Grant {
  grantee_id: string
  permission: Permission
  granted_at?: string
  expires_at?: string | null
  template_id?: string
  sandbox_id?: string
  [k: string]: unknown
}

export interface GrantOptions {
  perm?: Permission
  expiresAt?: string
  reason?: string
}

export interface NetworkOptions {
  allowInternetAccess?: boolean
  allowOut?: string[]
  denyOut?: string[]
  allowPublicTraffic?: boolean
  maskRequestHost?: boolean
}

export interface CreateSandboxOptions {
  /** Template name; defaults to `"base"` when neither name nor id is given. */
  template?: string
  templateId?: string
  /** Lifetime in seconds. 0 / omitted = no deadline. */
  timeout?: number
  onTimeout?: 'freeze' | 'kill'
  metadata?: Record<string, string>
  envVars?: Record<string, string>
  network?: NetworkOptions
}

export interface SandboxVersion {
  version: number
  frozenAt?: string
  label?: string
  [k: string]: unknown
}

/** Full sandbox record as returned by create / get / freeze / resume / rollback / clone / reset. */
export interface SandboxInfo {
  id: string
  templateId?: string
  template_id?: string
  state: SandboxState
  region?: string
  portUrl?: string
  domain?: string
  createdAt?: string
  startedAt?: string
  frozenAt?: string
  timeoutAt?: string
  cpuCount?: number
  memoryMB?: number
  diskSizeGB?: number
  idleTimeout?: number
  versions?: SandboxVersion[]
  metadata?: Record<string, string>
  trafficAccessToken?: string
  is_owner?: boolean
  has_write?: boolean
  cogen_id?: string
  created_at?: string
  updated_at?: string
  [k: string]: unknown
}

/** Compact row from `GET /sandboxes`. */
export interface SandboxSummary {
  id: string
  template_id?: string
  state: SandboxState
  region?: string
  portUrl?: string
  domain?: string
  cogen_id?: string
  created_at?: string
  updated_at?: string
  [k: string]: unknown
}

export interface MetricPoint {
  timestamp: string
  timestampUnix: number
  cpuCount: number
  cpuUsedPct: number
  memUsed: number
  memTotal: number
  memCache?: number
  diskUsed: number
  diskTotal: number
  [k: string]: unknown
}

export interface ExecOptions {
  cwd?: string
  envVars?: Record<string, string>
  user?: string
  /** Seconds, default 60, max 600. */
  timeout?: number
}

export interface CommandResult {
  stdout: string
  stderr: string
  exitCode: number
}

export interface ExecEvent {
  kind: 'stdout' | 'stderr' | 'exit' | 'error'
  /** Output chunk for stdout/stderr, error message for error. */
  data?: string
  exitCode?: number
  execId?: string
  stage?: string
}

export interface RunCodeOptions {
  language?: string
  contextId?: string
  envVars?: Record<string, string>
  /** Seconds, default 60, max 540. */
  timeout?: number
  images?: 'base64' | 'omit'
}

export interface CodeError {
  name: string
  value: string
  traceback: string | string[]
}

export interface CodeResult {
  stdout: string
  stderr: string
  executionCount?: number
  complete?: boolean
  error: CodeError | null
  results: Array<Record<string, unknown>>
  [k: string]: unknown
}

export interface StartCommandOptions {
  cwd?: string
  envVars?: Record<string, string>
  user?: string
}

export interface ProcessInfo {
  execId: string
  pid: number
  sandboxId?: string
  [k: string]: unknown
}

export interface FileEntry {
  name: string
  path: string
  isDir: boolean
  size: number
}

export interface PtyOptions {
  command?: string[]
  rows?: number
  cols?: number
  cwd?: string
  envVars?: Record<string, string>
}

export interface PtyEvent {
  kind: 'data' | 'exit'
  data?: string
  exitCode?: number
  reason?: string
}

export interface WaitOptions {
  /** Seconds. */
  timeout?: number
  /** Poll interval in seconds. */
  interval?: number
}

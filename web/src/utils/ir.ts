/**
 * 归档的 IR（中间表示）JSON 结构 —— Go 默认序列化，字段为 PascalCase。
 * 未设置的字段可能缺失或为零值（空字符串 / null），须按 Type 判断内容块种类。
 */

export interface IRSystemBlock {
  Text: string
}

export interface IRImage {
  URL: string
  Base64: string
  MediaType: string
}

export interface IRToolUse {
  ID: string
  Name: string
  /** json.RawMessage —— 可能是对象，也可能是字符串 */
  Input: unknown
}

export interface IRToolResult {
  ToolUseID: string
  Content: unknown
  IsError: boolean
}

export type IRContentType = 'text' | 'image' | 'tool_use' | 'tool_result' | 'thinking' | 'redacted_thinking'

export interface IRContentBlock {
  Type: IRContentType
  Text: string
  Thinking: string
  Signature: string
  Data: string
  Image: IRImage | null
  ToolUse: IRToolUse | null
  ToolResult: IRToolResult | null
}

export interface IRMessage {
  Role: 'user' | 'assistant'
  Content: IRContentBlock[]
}

export interface IRRequest {
  Model: string
  System?: IRSystemBlock[]
  Messages?: IRMessage[]
  Tools?: unknown[]
}

/** 对应 Go translate.Usage（无 json tag，PascalCase 序列化） */
export interface IRUsage {
  InputTokens?: number
  OutputTokens?: number
  CacheReadTokens?: number
  CacheCreationTokens?: number
  ReasoningTokens?: number
}

export interface IRResponse {
  ID: string
  Model: string
  Content?: IRContentBlock[]
  StopReason: string
  Usage?: IRUsage
}

/** 安全解析 IR JSON 文本；非法 JSON / 空串返回 null */
export function parseIR<T>(json: string): T | null {
  if (!json) return null
  try {
    return JSON.parse(json) as T
  } catch {
    return null
  }
}

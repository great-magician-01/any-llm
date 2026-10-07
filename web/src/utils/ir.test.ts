/**
 * ir.ts 的 parseIR：把归档的 IR JSON 文本安全地解析成对象。
 *
 * 守的规则：解析失败（空串 / 空白 / 非法 JSON）一律返回 null，让调用方走「无法
 * 展示原文」的分支；它只做 JSON 解析，不做结构校验——形状不对时由调用方按 Type
 * 字段判断（这里把这一点也固定下来，免得有人误以为它会兜底）。
 */
import { describe, expect, it } from 'vitest'
import { imageSrc, parseIR, type IRRequest, type IRResponse } from './ir'

describe('parseIR', () => {
  it('空串与纯空白返回 null（未归档 / 无内容）', () => {
    expect(parseIR('')).toBeNull()
    expect(parseIR('   ')).toBeNull()
  })

  it('非法 JSON 返回 null 而不是抛错', () => {
    expect(parseIR('{ not json')).toBeNull()
    expect(parseIR('undefined')).toBeNull()
    expect(parseIR('{"a":1,}')).toBeNull()
  })

  it('JSON 字面量 null 也返回 null', () => {
    expect(parseIR('null')).toBeNull()
  })

  it('解析出 IR 请求的 PascalCase 字段', () => {
    // 归档是 Go 默认序列化：字段名是 PascalCase，改小写就整块读不出来
    const raw = JSON.stringify({ Model: 'gpt-4o', System: [{ Text: 'sys' }], Messages: [{ Role: 'user', Content: [] }] })
    const req = parseIR<IRRequest>(raw)
    expect(req?.Model).toBe('gpt-4o')
    expect(req?.System?.[0].Text).toBe('sys')
    expect(req?.Messages?.[0].Role).toBe('user')
  })

  it('解析出 IR 响应并保留可能缺失的可选字段', () => {
    // Go 的 omitempty 会让空字段整个消失，界面必须能容忍缺字段
    const res = parseIR<IRResponse>(JSON.stringify({ ID: 'r1', Model: 'm', StopReason: 'stop' }))
    expect(res?.ID).toBe('r1')
    expect(res?.StopReason).toBe('stop')
    // 归档里缺 Content/Usage 是合法的（零值不序列化），parseIR 不补默认值
    expect(res?.Content).toBeUndefined()
    expect(res?.Usage).toBeUndefined()
  })

  it('不做结构校验：数组、数字、字符串照原样返回', () => {
    // 调用方（IrContent 等）自己按 Type 字段判断，parseIR 不做兜底也不会抛错
    expect(parseIR<number[]>('[]')).toEqual([])
    // 注意 0 是 falsy，但入参是非空字符串，所以照样返回 0（不是 null）
    expect(parseIR<number>('0')).toBe(0)
    expect(parseIR<string>('"x"')).toBe('x')
  })
})

describe('imageSrc', () => {
  it('URL 形态原样返回（含已是 data URL 的）', () => {
    expect(imageSrc({ URL: 'https://example.com/a.png', Base64: '', MediaType: '' }))
      .toBe('https://example.com/a.png')
    const dataURL = 'data:image/webp;base64,AAAA'
    expect(imageSrc({ URL: dataURL, Base64: '', MediaType: '' })).toBe(dataURL)
  })

  it('base64 形态组装成 data URL，MediaType 原样带上', () => {
    expect(imageSrc({ URL: '', Base64: 'QUJD', MediaType: 'image/jpeg' }))
      .toBe('data:image/jpeg;base64,QUJD')
  })

  it('base64 缺 MediaType 时回退 image/png', () => {
    expect(imageSrc({ URL: '', Base64: 'QUJD', MediaType: '' }))
      .toBe('data:image/png;base64,QUJD')
  })

  it('URL 优先于 Base64', () => {
    expect(imageSrc({ URL: 'https://example.com/a.png', Base64: 'QUJD', MediaType: 'image/png' }))
      .toBe('https://example.com/a.png')
  })

  it('空值 / 两个字段都为空返回空串', () => {
    expect(imageSrc(null)).toBe('')
    expect(imageSrc(undefined)).toBe('')
    expect(imageSrc({ URL: '', Base64: '', MediaType: '' })).toBe('')
  })
})

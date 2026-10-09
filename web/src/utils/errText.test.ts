import { describe, expect, it } from 'vitest'
import { errText } from './errText'

describe('errText', () => {
  it('axios 风格错误优先取服务端 error 字段', () => {
    expect(errText({ response: { data: { error: 'model not allowed' } } })).toBe('model not allowed')
  })

  it('error 字段非字符串也字符串化', () => {
    expect(errText({ response: { data: { error: 42 } } })).toBe('42')
  })

  it('响应体是纯文本时原样返回', () => {
    expect(errText({ response: { data: 'bad gateway' } })).toBe('bad gateway')
  })

  it('响应体是无 error 字段的对象时 JSON 序列化', () => {
    expect(errText({ response: { data: { detail: 'x' } } })).toBe('{"detail":"x"}')
  })

  it('没有响应体时退回 message', () => {
    expect(errText(new Error('Network Error'))).toBe('Network Error')
    expect(errText({ message: 'timeout' })).toBe('timeout')
  })

  it('空 message 与其他值都兜底为字符串', () => {
    expect(errText({ message: '' })).toBe('[object Object]')
    expect(errText('boom')).toBe('boom')
    expect(errText(undefined)).toBe('undefined')
  })
})

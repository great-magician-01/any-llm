/**
 * yaml.ts 的纯量转义规则（omp / dsh 两套客户端配置生成器共用）。
 *
 * 生成的 YAML 是用户直接粘进客户端配置里的，一旦转义少了引号，模型名或 key 会被
 * YAML 解析成别的意思（最常见：带 ': ' 的值被当成嵌套映射）——静默配置错误。
 */
import { describe, expect, it } from 'vitest'
import { yamlScalar } from './yaml'

describe('yamlScalar', () => {
  // 规则：能裸写就裸写（生成的文件给人看），但指示符/空白开头会被 YAML 误解析
  it('普通纯量不加引号', () => {
    expect(yamlScalar('gpt-4o')).toBe('gpt-4o')
    expect(yamlScalar('https://api.example.com/v1')).toBe('https://api.example.com/v1')
    expect(yamlScalar('a#b')).toBe('a#b') // 紧跟非空白的 # 属于纯量本身
    expect(yamlScalar('模型')).toBe('模型')
  })

  // 规则：空串必须显式写成 ''，否则 YAML 里 `key:` 会解析成 null
  it('空串输出空引号串（否则 YAML 里 key: 会变成 null）', () => {
    expect(yamlScalar('')).toBe("''")
  })

  // 规则：首字符是 YAML 指示符、或前后有空白，都必须加引号
  it('以指示符开头、或带前后空白时加双引号', () => {
    expect(yamlScalar('-x')).toBe('"-x"')
    expect(yamlScalar('#x')).toBe('"#x"')
    expect(yamlScalar('?x')).toBe('"?x"')
    expect(yamlScalar(' leading')).toBe('" leading"')
    expect(yamlScalar('trailing ')).toBe('"trailing "')
    expect(yamlScalar('{a}')).toBe('"{a}"')
  })

  // 规则：'# ' 是 YAML 的行内注释开始，含它的值必须加引号
  it('含 ": " 的值必须加引号（否则被解析成嵌套映射）', () => {
    expect(yamlScalar('a: b')).toBe('"a: b"')
  })

  // 规则：换行/制表符等控制字符在裸纯量里非法，必须进双引号串
  it('控制字符/换行必须加引号并把换行写成 \\n', () => {
    expect(yamlScalar('a\nb')).toBe('"a\\nb"')
    expect(yamlScalar('a\tb')).toBe('"a\tb"')
  })

  // 规则：进了双引号串的 \ 与 " 必须再转义一层（否则 YAML 串会提前结束）
  it('加了引号的值里，反斜杠与双引号要再转义一层', () => {
    // 触发加引号的是 ': '，转义的是串里的 \ 与 "
    expect(yamlScalar('a: b\\c"d')).toBe('"a: b\\\\c\\"d"')
  })

  // 规则：'#' 只在「空白之后」才是注释开始；夹在词中的 # 与 " 属于纯量本身，不必加引号
  it('纯量中间的双引号本身合法，不需要为此加引号', () => {
    expect(yamlScalar('a"c')).toBe('a"c')
  })

  /**
   * 回归位：「空白 + #」在 YAML 里是行内注释的开始，'a #b' 原样输出会让值变成 'a'
   * （生成的配置被静默改错）。判定必须把它加引号。
   */
  it('空白后紧跟 # 的值必须加引号（否则 YAML 当注释吞掉后半段）', () => {
    expect(yamlScalar('a #b')).toBe('"a #b"')
    expect(yamlScalar('model #1')).toBe('"model #1"')
    // 不构成注释的 # 仍保持裸纯量
    expect(yamlScalar('a#b')).toBe('a#b')
    expect(yamlScalar('https://x/#frag')).toBe('https://x/#frag')
  })
})

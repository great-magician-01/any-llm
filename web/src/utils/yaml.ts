/**
 * YAML 纯量输出规则，供各客户端配置生成器（omp / dsh）共用——
 * 规则只维护这一份，不要复制回生成器。
 */

/** 按需双引号转义，其余输出为合法 YAML 纯量 */
export function yamlScalar(v: string): string {
  if (v === '') return "''"
  const plain =
    !/^[\s\-?:,\[\]{}#&*!|>'"%@`]/.test(v) && // 不以指示符开头
    !/^\s/.test(v) && // 无前导空白
    !/\s$/.test(v) && // 无尾部空白
    !/[\x00-\x1f\x7f]/.test(v) && // 无控制字符/换行
    !/[:#] /.test(v) // 无 ': ' / ' #'（行内映射/注释）
  if (plain) return v
  return '"' + v.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n') + '"'
}

import DOMPurify from 'dompurify'

export type QuickTestPresetId = 'hello' | 'pelican' | 'candy'

export const QUICK_TEST_PROMPTS: Record<QuickTestPresetId, string> = {
  hello: 'Hello',
  pelican: '只返回一个完整 SVG：绘制一只鹈鹕骑自行车环游地球的精美 2D 无缝循环动画。使用横向 viewBox，完整显示鹈鹕长嘴、喉囊、身体和整辆车；车架连接清晰，双脚踩踏板、车轮转动，分层景物移动形成骑行感。画面明亮，主体突出。可用内联 CSS 或 SMIL；不要 Markdown、HTML、JavaScript、外部图片、字体或其他外部资源。',
  candy: '黑袋中有三种口味、两种可凭手感区分的形状的糖果，但不能凭手感区分口味。数量如下：圆形：苹果 7、桃子 9、西瓜 8；五角星形：苹果 7、桃子 6、西瓜 4。活动前可以预定各取多少颗圆形和五角星形。最少共取多少颗，才能保证同时拥有“圆形苹果 + 五角星桃子”或“圆形桃子 + 五角星苹果”？给出简短解法，并在最后单独一行输出 FINAL_ANSWER: 整数。'
}

export const CANDY_EXPECTED_ANSWER = 21

export function getQuickTestPreset(prompt: string): QuickTestPresetId | null {
  const trimmed = prompt.trim()
  return (Object.keys(QUICK_TEST_PROMPTS) as QuickTestPresetId[])
    .find(preset => QUICK_TEST_PROMPTS[preset] === trimmed) ?? null
}

function unwrapAnswerLine(line: string): string {
  return line.trim().replace(/^(?:[-*]\s+|#{1,6}\s+)/, '').replace(/\*\*|__|`/g, '').trim()
}

export function evaluateCandyAnswer(content: string): {
  status: 'correct' | 'incorrect' | 'ungraded'
  answer: number | null
} {
  const lines = content.trim().split(/\r?\n/)
  if (/^`{3,}\s*$/.test(lines[lines.length - 1] ?? '')) lines.pop()
  const lastLine = unwrapAnswerLine(lines[lines.length - 1] ?? '')
  const match = /^FINAL_ANSWER\s*[:：]\s*(-?\d+)$/i.exec(lastLine)
  if (!match) return { status: 'ungraded', answer: null }
  const answer = Number(match[1])
  if (!Number.isSafeInteger(answer)) return { status: 'ungraded', answer: null }
  return { status: answer === CANDY_EXPECTED_ANSWER ? 'correct' : 'incorrect', answer }
}

const SVG_NAMESPACE = 'http://www.w3.org/2000/svg'
const XLINK_NAMESPACE = 'http://www.w3.org/1999/xlink'
const XML_NAMESPACE = 'http://www.w3.org/XML/1998/namespace'
const XMLNS_NAMESPACE = 'http://www.w3.org/2000/xmlns/'
const ANIMATED_ATTRIBUTES = new Set([
  'transform', 'd', 'x', 'y', 'x1', 'y1', 'x2', 'y2', 'cx', 'cy', 'r', 'rx', 'ry',
  'width', 'height', 'opacity', 'fill', 'fill-opacity', 'stroke', 'stroke-width',
  'stroke-opacity', 'stroke-dashoffset', 'stroke-dasharray', 'points', 'offset',
  'stop-color', 'stop-opacity', 'visibility', 'color', 'font-size', 'letter-spacing'
])

function extractSvg(content: string): { candidate: string | null; started: boolean } {
  // Ignore comments/CDATA when locating a root and count nested SVG viewports.
  const tags = /<!--[\s\S]*?-->|<!\[CDATA\[[\s\S]*?\]\]>|<\?[\s\S]*?\?>|<\/?svg\b(?:[^"'<>]|"[^"]*"|'[^']*')*>/gi
  let start = -1
  let depth = 0
  for (const match of content.matchAll(tags)) {
    const tag = match[0]
    if (!/^<\/?svg\b/i.test(tag)) continue
    if (/^<\//.test(tag)) {
      if (start < 0) continue
      depth -= 1
    } else {
      if (start < 0) start = match.index!
      if (!/\/\s*>$/.test(tag)) depth += 1
    }
    if (start >= 0 && depth === 0) {
      return { candidate: content.slice(start, match.index! + tag.length), started: true }
    }
  }
  return { candidate: null, started: start >= 0 || /<svg\b/i.test(content) }
}

function isLocalReference(value: string): boolean {
  return /^#[^\s"'()<>`]+$/.test(value.trim())
}

function hasUnsafeCss(value: string): boolean {
  // Decode CSS escapes before checking fetch-capable syntax, including obfuscated url/@import.
  const css = value
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\\(?:\r\n|[\n\r\f])/g, '')
    .replace(/\\([0-9a-f]{1,6})(?:\r\n|[ \t\r\n\f])?/gi, (_, hex: string) => {
      const point = parseInt(hex, 16)
      return point > 0 && point <= 0x10ffff ? String.fromCodePoint(point) : '\ufffd'
    })
    .replace(/\\([\s\S])/g, '$1')

  if (/@(?!keyframes\b|-webkit-keyframes\b|media\b|supports\b)/i.test(css)) return true
  if (/(?:expression|(?:-webkit-)?image-set|image|cross-fade|paint)\s*\(|(?:-moz-binding|behavior)\s*:|(?:https?|ftp|file|data|javascript|vbscript)\s*:/i.test(css)) return true

  const urls = /\burl\s*\(\s*(?:"([^"]*)"|'([^']*)'|([^)]*))\s*\)/gi
  let urlCount = 0
  for (const match of css.matchAll(urls)) {
    urlCount += 1
    if (!isLocalReference(match[1] ?? match[2] ?? match[3] ?? '')) return true
  }
  return urlCount !== (css.match(/\burl\s*\(/gi)?.length ?? 0)
}

function parseSvg(source: string): Element | null {
  const doc = new DOMParser().parseFromString(source, 'image/svg+xml')
  const root = doc.documentElement
  if (doc.getElementsByTagName('parsererror').length || root.localName !== 'svg') return null
  if (root.namespaceURI && root.namespaceURI !== SVG_NAMESPACE) return null
  return root
}

function cleanSvgTree(root: Element): void {
  for (const element of [root, ...Array.from(root.querySelectorAll('*'))]) {
    const tag = element.localName.toLowerCase()
    if (element.namespaceURI !== SVG_NAMESPACE) {
      element.remove()
      continue
    }
    if (tag === 'style' && hasUnsafeCss(element.textContent ?? '')) {
      element.remove()
      continue
    }
    if ((tag === 'animate' || tag === 'animatetransform') &&
      !ANIMATED_ATTRIBUTES.has((element.getAttribute('attributeName') ?? '').toLowerCase())) {
      element.remove()
      continue
    }
    for (const attribute of Array.from(element.attributes)) {
      const name = attribute.localName.toLowerCase()
      const namespace = attribute.namespaceURI
      const value = attribute.value
      if (namespace === XMLNS_NAMESPACE &&
        (value === SVG_NAMESPACE || value === XLINK_NAMESPACE)) continue
      const knownNamespace = !namespace || namespace === XLINK_NAMESPACE ||
        namespace === XML_NAMESPACE || namespace === XMLNS_NAMESPACE
      const unsafeNamespace = !knownNamespace || (namespace === XMLNS_NAMESPACE &&
        value !== SVG_NAMESPACE && value !== XLINK_NAMESPACE)
      if (unsafeNamespace || name.startsWith('on') || name === 'base' ||
        ['src', 'srcset', 'poster', 'action', 'formaction', 'background'].includes(name) ||
        (name === 'href' && !isLocalReference(value)) || hasUnsafeCss(value)) {
        element.removeAttributeNode(attribute)
      }
    }
  }
  root.setAttributeNS(XMLNS_NAMESPACE, 'xmlns', SVG_NAMESPACE)
}

export function prepareSvgPreview(content: string): {
  status: 'ready' | 'missing' | 'invalid'
  src: string | null
} {
  const { candidate, started } = extractSvg(content)
  if (!candidate) return { status: started ? 'invalid' : 'missing', src: null }
  if (typeof DOMParser === 'undefined' || typeof XMLSerializer === 'undefined') {
    return { status: 'invalid', src: null }
  }

  try {
    const parsed = parseSvg(candidate)
    if (/<!DOCTYPE|<!ENTITY/i.test(candidate) || !parsed) {
      return { status: 'invalid', src: null }
    }
    // Models occasionally omit xmlns; put the complete tree in the SVG namespace before parsing again.
    const normalized = parsed.namespaceURI ? candidate : candidate.replace(
      /^<svg\b(?:[^"'<>]|"[^"]*"|'[^']*')*>/i,
      opening => opening.replace(/\s+xmlns\s*=\s*(["'])\1/i, '').replace(/^<svg\b/i, `<svg xmlns="${SVG_NAMESPACE}"`)
    )
    const sanitized = DOMPurify.sanitize(normalized, {
      USE_PROFILES: { svg: true, svgFilters: true },
      ADD_TAGS: ['style', 'animate', 'animateMotion', 'animateTransform', 'mpath', 'use'],
      ADD_ATTR: [
        'attributeName', 'attributeType', 'begin', 'dur', 'end', 'repeatCount', 'repeatDur',
        'from', 'to', 'by', 'values', 'keyTimes', 'keySplines', 'keyPoints', 'calcMode',
        'additive', 'accumulate', 'path', 'rotate'
      ],
      FORBID_TAGS: [
        'script', 'foreignObject', 'foreignobject', 'a', 'image', 'feImage', 'feimage',
        'iframe', 'object', 'embed', 'audio', 'video', 'link', 'set', 'discard'
      ],
      ALLOW_DATA_ATTR: false,
      ALLOW_ARIA_ATTR: false
    })
    const root = parseSvg(sanitized)
    if (!root) return { status: 'invalid', src: null }
    cleanSvgTree(root)
    const source = new XMLSerializer().serializeToString(root)
    return { status: 'ready', src: `data:image/svg+xml;charset=utf-8,${encodeURIComponent(source)}` }
  } catch {
    return { status: 'invalid', src: null }
  }
}

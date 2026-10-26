import { describe, expect, it } from 'vitest'
import {
  CANDY_EXPECTED_ANSWER,
  QUICK_TEST_PROMPTS,
  evaluateCandyAnswer,
  getQuickTestPreset,
  prepareSvgPreview
} from '../availabilityQuickTests'

function previewSource(content: string): string {
  const preview = prepareSvgPreview(content)
  expect(preview.status).toBe('ready')
  expect(preview.src).toMatch(/^data:image\/svg\+xml;charset=utf-8,/)
  const source = decodeURIComponent(preview.src!.split(',').slice(1).join(','))
  expect(new DOMParser().parseFromString(source, 'image/svg+xml').getElementsByTagName('parsererror').length).toBe(0)
  return source
}

describe('availability quick test presets', () => {
  it('recognizes only exact preset prompts, with surrounding whitespace allowed', () => {
    expect(QUICK_TEST_PROMPTS.hello).toBe('Hello')
    for (const preset of ['hello', 'pelican', 'candy'] as const) {
      expect(getQuickTestPreset(`\n${QUICK_TEST_PROMPTS[preset]} `)).toBe(preset)
    }
    expect(getQuickTestPreset('hello')).toBeNull()
    expect(getQuickTestPreset(`${QUICK_TEST_PROMPTS.candy} 请给 21`)).toBeNull()
    expect(getQuickTestPreset('')).toBeNull()
  })

  it('states that the candy shapes can be selected before drawing', () => {
    expect(QUICK_TEST_PROMPTS.candy).toContain('预定各取多少颗圆形和五角星形')
    expect(QUICK_TEST_PROMPTS.candy).not.toContain('21')
    expect(CANDY_EXPECTED_ANSWER).toBe(21)
  })

  it('derives the minimum 21 from all possible failure cases for the fixed inventory', () => {
    const guaranteed: { round: number; star: number; total: number }[] = []
    for (let round = 0; round <= 24; round += 1) {
      for (let star = 0; star <= 17; star += 1) {
        const canFail =
          (round <= 8 && star <= 17) || // No round apple/peach.
          (round <= 15 && star <= 11) || // Only apple/watermelon in both shapes.
          (round <= 17 && star <= 10) || // Only peach/watermelon in both shapes.
          (round <= 24 && star <= 4) // No star apple/peach.
        if (!canFail) guaranteed.push({ round, star, total: round + star })
      }
    }
    expect(Math.min(...guaranteed.map(choice => choice.total))).toBe(CANDY_EXPECTED_ANSWER)
    expect(guaranteed.filter(choice => choice.total === CANDY_EXPECTED_ANSWER))
      .toEqual([{ round: 9, star: 12, total: 21 }])
  })
})

describe('candy answer evaluation', () => {
  it.each([
    '取 9 个圆形与 12 个五角星形，共 21 个。\nFINAL_ANSWER: 21',
    'FINAL_ANSWER: 21\n\n',
    '**FINAL_ANSWER: 21**',
    '**FINAL_ANSWER:** **21**',
    '`FINAL_ANSWER: 21`',
    '```text\nFINAL_ANSWER: 21\n```',
    '- FINAL_ANSWER：21'
  ])('grades an explicit final integer as correct: %s', content => {
    expect(evaluateCandyAnswer(content)).toEqual({ status: 'correct', answer: 21 })
  })

  it.each([20, 22, 27, 121, -1])('grades a final wrong integer %s as incorrect', answer => {
    expect(evaluateCandyAnswer(`推理中提及 21。\nFINAL_ANSWER: ${answer}`))
      .toEqual({ status: 'incorrect', answer })
  })

  it.each([
    '', '21', '答案是 21', 'FINAL_ANSWER: 21\n可能还需要再检查。',
    'FINAL_ANSWER: 21 或 22', 'FINAL_ANSWER: 21/22', 'FINAL_ANSWER: 21, 22',
    'FINAL_ANSWER: 21.0', 'FINAL_ANSWER: 2.1e1', 'FINAL_ANSWER: 不确定',
    'FINAL_ANSWER: 9007199254740992', '共有 21 个，27 个也可以。'
  ])('leaves missing, ambiguous and noninteger answers ungraded: %s', content => {
    expect(evaluateCandyAnswer(content)).toEqual({ status: 'ungraded', answer: null })
  })
})

describe('SVG preview preparation', () => {
  it('accepts raw or fenced SVG and extracts the first complete drawing', () => {
    const raw = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 450"><circle cx="50" cy="50" r="20"/></svg>'
    expect(previewSource(raw)).toContain('viewBox="0 0 800 450"')
    const source = previewSource(`这是图像：\n\`\`\`svg\n${raw}\n\`\`\`\n<svg><text>SECOND</text></svg>`)
    expect(source).toContain('<circle')
    expect(source).not.toContain('SECOND')
  })

  it('handles nested SVG viewports, root namespace insertion and comments', () => {
    const source = previewSource('<!-- <svg></svg> --><svg viewBox="0 0 30 30"><svg x="2"><circle r="3"/></svg><path d="M0 0L1 1"/></svg>')
    expect(source).toContain('xmlns="http://www.w3.org/2000/svg"')
    expect(source).toContain('<path')
    expect(previewSource('<svg/>')).toContain('<svg')
  })

  it.each(['', '没有返回绘图', '```text\nhello\n```'])('reports a missing drawing: %s', content => {
    expect(prepareSvgPreview(content)).toEqual({ status: 'missing', src: null })
  })

  it.each([
    '<svg><path', '<svg><g></svg>', '<svg><path></svg>', '<svg',
    '<svg xmlns="https://invalid.example"><circle/></svg>',
    '<svg><text>&unknown;</text></svg>'
  ])('rejects malformed, truncated or incorrectly namespaced SVG: %s', content => {
    expect(prepareSvgPreview(content)).toEqual({ status: 'invalid', src: null })
  })

  it('removes scripts, embedded HTML, event handlers, external links and images', () => {
    const source = previewSource(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" onload="alert(1)" xml:base="https://evil.example/">
      <script>alert(1)</script><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">unsafe</div></foreignObject>
      <a href="https://evil.example"><rect width="10" height="10"/></a>
      <image href="https://evil.example/image.png"/><feImage xlink:href="data:image/svg+xml,unsafe"/>
      <use href="//evil.example/x.svg#x"/><path onmouseover="alert(1)" fill="url(https://evil.example/fill.svg#x)"/>
      <animate attributeName="href" values="#safe;https://evil.example" dur="1s"/>
    </svg>`)
    expect(source).not.toMatch(/script|foreignObject|onload|onmouseover|xml:base|<a\b|<image|<feImage|evil\.example|data:image\/svg\+xml,unsafe/i)
    expect(source).not.toContain('attributeName="href"')
    expect(source).toContain('<rect')
  })

  it('keeps no foreign namespaces or external xlink references', () => {
    const source = previewSource(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
      <defs><path id="route" d="M0 0L10 10"/></defs>
      <use xlink:href="#route"/><use xlink:href="//evil.example/route.svg#route"/>
      <g xmlns="https://evil.example/namespace"><rect width="1" height="1"/></g>
    </svg>`)
    expect(source).toContain('#route')
    expect(source).not.toContain('evil.example')
  })

  it.each([
    'fill: url(https://evil.example/a)',
    'fill: u\\72l(//evil.example/a)',
    'fill: u/**/rl("https://evil.example/a")',
    '@import "https://evil.example/a.css";',
    '@\\69mport "//evil.example/a.css";',
    '@font-face {font-family:x;src:url(https://evil.example/font)}',
    'background-image: image-set("//evil.example/a.png" 1x);',
    'fill: url("#safe") url(//evil.example/a);',
    'fill: url("#safe"'
  ])('removes unsafe or malformed CSS resources: %s', css => {
    const escaped = css.replace(/&/g, '&amp;').replace(/"/g, '&quot;')
    const source = previewSource(`<svg><style><![CDATA[${css}]]></style><path style="${escaped}" d="M0 0L2 2"/></svg>`)
    expect(source).not.toContain('<style')
    expect(source).not.toContain('style=')
    expect(source).toContain('<path')
  })

  it('preserves local defs references, safe inline CSS and looping CSS/SMIL animations', () => {
    const source = previewSource(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 450">
      <defs><linearGradient id="sky"><stop offset="0" stop-color="#aaf"/></linearGradient><path id="road" d="M0 50L800 50"/></defs>
      <style>@keyframes wheel { from { transform:rotate(0deg) } to { transform:rotate(360deg) } } .wheel { animation:wheel 2s linear infinite; fill:url(#sky); }</style>
      <g class="wheel" style="fill: url('#sky'); transform-origin: 40px 40px"><circle r="20"/></g>
      <use href="#road"/>
      <circle r="10"><animate attributeName="cy" values="10;12;10" dur="2s" repeatCount="indefinite"/><animateTransform attributeName="transform" type="rotate" from="0" to="360" dur="2s" repeatCount="indefinite"/></circle>
      <g><animateMotion dur="8s" repeatCount="indefinite"><mpath href="#road"/></animateMotion></g>
    </svg>`)
    expect(source).toContain('@keyframes wheel')
    expect(source).toContain('animation:wheel 2s linear infinite')
    expect(source).toContain('url(#sky)')
    expect(source).toContain('href="#road"')
    expect(source).toContain('<animate ')
    expect(source).toContain('<animateTransform ')
    expect(source).toContain('<animateMotion ')
    expect(source).toContain('<mpath ')
    expect(source).toContain('repeatCount="indefinite"')
  })
})

#!/usr/bin/env python3
"""Build the public MCP documentation site (requires Markdown==3.7)."""
from html import escape
from pathlib import Path
import re
import shutil
import markdown
from markdown.extensions.toc import slugify_unicode

root = Path(__file__).resolve().parent
preview = root / 'site'
preview.mkdir(exist_ok=True)
source = (root / 'user-guide.md').read_text(encoding='utf-8')
# The sidebar replaces the source's repeated, manual table of contents.
source = re.sub(r'\n## 目录\n[\s\S]*?(?=\n## 1\.)', '\n', source, count=1)
renderer = markdown.Markdown(extensions=['tables', 'fenced_code', 'toc', 'sane_lists'],
    extension_configs={'toc': {'slugify': slugify_unicode, 'toc_depth': '2-3'}})
body = renderer.convert(source)
navigation = []
for section in renderer.toc_tokens:
    if section['level'] != 2:
        continue
    title = re.sub(r'^\d+\.\s*', '', section['name'])
    navigation.append('<a href="#' + escape(section['id'], quote=True) + '">' + escape(title) + '</a>')
if not navigation:
    for section in renderer.toc_tokens:
        for child in section.get('children', []):
            title = re.sub(r'^\d+\.\s*', '', child['name'])
            navigation.append('<a href="#' + escape(child['id'], quote=True) + '">' + escape(title) + '</a>')
assert len(navigation) == 12, 'all guide sections must remain navigable'

template = r'''<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta http-equiv="Content-Security-Policy" content="default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'none'; base-uri 'none'; form-action 'none'">
<title>流星创作台 · MCP 接入文档</title>
<link rel="canonical" href="https://api.meteor21c.fun/docs/mcp/">
<style>
:root{--bg:#fff;--panel:#f8f9fc;--ink:#202638;--muted:#697386;--border:#e8ebf1;--accent:#6357d7;--accent-soft:#f0edff;--code:#f5f6fa;--head:#f8f9fc;color-scheme:light}
*{box-sizing:border-box}html{scroll-behavior:smooth;scroll-padding-top:94px}body{margin:0;background:var(--bg);color:var(--ink);font:15px/1.85 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;-webkit-font-smoothing:antialiased}
a{color:var(--accent);text-decoration:none}a:hover{text-decoration:underline}button{font:inherit;cursor:pointer}header{height:66px;border-bottom:1px solid var(--border);position:sticky;top:0;z-index:10;background:var(--bg)}.header-inner{max-width:1336px;height:100%;margin:auto;padding:0 36px;display:flex;align-items:center;justify-content:space-between;gap:20px}.brand{display:flex;align-items:center;gap:11px;font-size:16px;font-weight:700;color:var(--ink);white-space:nowrap}.mark{width:28px;height:28px;border-radius:9px;background:linear-gradient(145deg,#7b70ee,#4a42a4);position:relative}.mark:before{content:"";position:absolute;inset:7px;background:#fff;clip-path:polygon(50% 0,61% 39%,100% 50%,61% 61%,50% 100%,39% 61%,0 50%,39% 39%)}.divider{height:17px;width:1px;background:var(--border);margin:0 5px}.brand small{font-size:13px;font-weight:400;color:var(--muted)}.toplinks{display:flex;align-items:center;gap:24px;font-size:13px}.toplinks a{color:var(--muted)}.toplinks a:last-child{color:var(--accent)}
.layout{max-width:1336px;margin:auto;display:grid;grid-template-columns:232px minmax(0,1fr);column-gap:54px;padding:38px 36px 80px}.sidebar{position:sticky;top:100px;height:calc(100vh - 130px);overflow-y:auto;padding-right:15px}.nav-label{font-size:11px;letter-spacing:1.2px;color:var(--muted);font-weight:600;margin:0 0 13px 12px}.nav a{display:block;font-size:13px;color:var(--muted);padding:7px 12px;border-radius:7px;margin:2px 0;transition:background .12s}.nav a:hover{background:var(--panel);color:var(--ink);text-decoration:none}.nav a.active{background:var(--accent-soft);color:var(--accent);font-weight:600}.side-resources{border-top:1px solid var(--border);margin-top:25px;padding:20px 12px;font-size:12px;display:grid;gap:8px}.side-resources span{color:var(--muted);margin-bottom:3px}.content{min-width:0;max-width:920px}.breadcrumb{font-size:12px;color:var(--muted);margin-bottom:10px}.breadcrumb span{padding:0 8px;color:#b5bccc}.tagline{display:flex;flex-wrap:wrap;gap:8px;margin:17px 0 25px}.tagline span{font-size:11px;font-weight:500;border:1px solid var(--border);padding:2px 9px;border-radius:5px;color:var(--muted)}article>h1{font-size:34px;line-height:1.4;letter-spacing:-.6px;font-weight:750;margin:0 0 17px}article>p:first-of-type{font-size:16px;color:var(--muted);line-height:1.9;margin:0 0 13px}article h2{font-size:23px;line-height:1.6;font-weight:650;padding-top:15px;margin:41px 0 18px;border-top:1px solid var(--border);padding-bottom:0}article h3{font-size:17px;line-height:1.65;margin:26px 0 12px;font-weight:650}p{margin:12px 0}ul,ol{padding-left:24px;margin:12px 0}li{padding-left:3px;margin:5px 0}strong{font-weight:650}hr{border:0;border-top:1px solid var(--border);margin:34px 0}code{font-family:"SFMono-Regular",Consolas,"Liberation Mono",monospace;font-size:.86em;word-break:break-word;background:var(--code);border-radius:4px;padding:2px 5px}pre{position:relative;background:var(--code);border:1px solid var(--border);border-radius:9px;padding:19px 20px;overflow:auto;font-size:13px;line-height:1.7;margin:15px 0 22px}pre code{display:block;padding:0;background:none;white-space:pre;word-break:normal;font-size:inherit;line-height:inherit}pre .copy-code{position:absolute;right:10px;top:9px;background:var(--bg);color:var(--muted);border:1px solid var(--border);border-radius:5px;font-size:10px;padding:3px 8px;opacity:0;transition:opacity .12s}pre:hover .copy-code,pre:focus-within .copy-code{opacity:1}.table-wrap{overflow-x:auto;max-width:100%;border:1px solid var(--border);border-radius:8px;margin:16px 0 22px}table{width:100%;border-collapse:collapse;font-size:12.5px;line-height:1.8;text-align:left}th{background:var(--head);font-weight:600}th,td{padding:10px 13px;vertical-align:top;border-bottom:1px solid var(--border)}tr:last-child td{border-bottom:0}td:first-child{min-width:110px}td code{font-size:11.3px}blockquote{margin:18px 0;padding:4px 16px;border-left:3px solid var(--accent);background:var(--panel)}.footer{color:var(--muted);font-size:12px;margin-top:45px;padding-top:18px;border-top:1px solid var(--border)}.mobile-menu{display:none}.back-top{display:inline-block;margin-top:15px;font-size:12px}
@media(prefers-color-scheme:dark){:root{--bg:#14151c;--panel:#1b1d27;--ink:#e5e7f0;--muted:#a2a8bb;--border:#2c2f3d;--accent:#afa4ff;--accent-soft:#2b2643;--code:#1c1e29;--head:#1f212d;color-scheme:dark}}
@media(max-width:1050px){.layout{column-gap:28px;grid-template-columns:195px minmax(0,1fr);padding-left:24px;padding-right:24px}.header-inner{padding:0 24px}article>h1{font-size:29px}.toplinks{gap:15px}}
@media(max-width:760px){header{height:60px}.header-inner{padding:0 18px}.brand{font-size:14px}.brand small,.divider{display:none}.toplinks{gap:14px;font-size:11px}.toplinks a:nth-child(2){display:none}.layout{display:block;padding:22px 18px 55px}.sidebar{display:none}.mobile-menu{display:block;margin:0 0 25px;border:1px solid var(--border);border-radius:8px;padding:9px 12px;font-size:13px;color:var(--muted)}.mobile-menu summary{cursor:pointer}.mobile-menu .nav{margin-top:12px;display:grid;grid-template-columns:1fr 1fr;gap:2px}.mobile-menu .nav a{font-size:12px;padding:5px 7px}article>h1{font-size:27px}article>p:first-of-type{font-size:14px}body{font-size:14px}article h2{font-size:21px}article h3{font-size:16px}pre{padding:17px 13px;font-size:11px}pre .copy-code{opacity:1}th,td{padding:8px 10px}table{font-size:12px}html{scroll-padding-top:78px}}
@media print{header,.sidebar,.mobile-menu,.copy-code,.back-top{display:none!important}.layout{display:block;padding:0}.content{max-width:none}body{font-size:11pt;color:#111;background:#fff}pre{white-space:pre-wrap}pre code{white-space:pre-wrap}.table-wrap{overflow:visible}h2,h3{break-after:avoid}table,pre{break-inside:avoid}}
</style>
</head>
<body id="top">
<header><div class="header-inner"><a class="brand" href="#top"><span class="mark" aria-hidden="true"></span>流星 API<span class="divider"></span><small>开发文档</small></a><div class="toplinks"><a href="https://api.meteor21c.fun/studio">流星创作台 ↗</a><a href="https://api.meteor21c.fun/studio/pricing">计价标准</a><a href="guide.md" download>下载 Markdown ↓</a></div></div></header>
<div class="layout"><aside class="sidebar" aria-label="文档目录"><div class="nav-label">MCP 接入指南</div><nav class="nav">__NAV__</nav><div class="side-resources"><span>开发资源</span><a href="mcp-tools.json" download>工具参数 JSON ↓</a><a href="request-examples.http" download>完整请求示例 ↓</a><a class="back-top" href="#top">返回顶部 ↑</a></div></aside><main class="content"><div class="breadcrumb">开发文档<span>/</span>MCP<span>/</span>图片与视频生成</div><details class="mobile-menu"><summary>文档目录</summary><nav class="nav">__NAV__</nav></details><article>__BODY__</article><div class="footer">流星 API · 图片与视频 MCP 接入文档<a class="back-top" style="float:right;margin:0" href="#top">返回顶部 ↑</a></div></main></div>
<script>
document.querySelectorAll('article table').forEach(table=>{const box=document.createElement('div');box.className='table-wrap';table.before(box);box.append(table)});
document.querySelectorAll('article pre').forEach(pre=>{const code=pre.querySelector('code');if(!code)return;const button=document.createElement('button');button.className='copy-code';button.type='button';button.textContent='复制';button.setAttribute('aria-label','复制代码');button.addEventListener('click',async()=>{try{await navigator.clipboard.writeText(code.textContent);button.textContent='已复制';setTimeout(()=>button.textContent='复制',1500)}catch{button.textContent='请选中复制'}});pre.append(button)});
const headings=[...document.querySelectorAll('article h2[id]')];
const observer=new IntersectionObserver(entries=>{const visible=entries.filter(e=>e.isIntersecting);if(!visible.length)return;const id=visible[0].target.id;document.querySelectorAll('.nav a').forEach(a=>a.classList.toggle('active',decodeURIComponent(a.hash.slice(1))===id))},{rootMargin:'-10% 0px -65% 0px'});
headings.forEach(heading=>observer.observe(heading));
document.querySelectorAll('.mobile-menu a').forEach(a=>a.addEventListener('click',()=>document.querySelector('.mobile-menu').open=false));
const first=document.querySelector('article>h1');if(first){const tags=document.createElement('div');tags.className='tagline';tags.innerHTML='<span>4 个媒体工具</span><span>Streamable HTTP</span><span>API Key 鉴权</span>';first.after(tags)}
</script>
</body></html>
'''
html = template.replace('__NAV__', ''.join(navigation)).replace('__BODY__', body)
(preview / 'index.html').write_text(html, encoding='utf-8')
for source_name, target_name in [('user-guide.md', 'guide.md'),
                                 ('mcp-tools.json', 'mcp-tools.json'),
                                 ('request-examples.http', 'request-examples.http')]:
    shutil.copyfile(root / source_name, preview / target_name)
print(str(preview / 'index.html'))

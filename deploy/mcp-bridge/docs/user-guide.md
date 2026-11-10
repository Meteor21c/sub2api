# 流星创作台 MCP 接入指南

通过 MCP 在智能体或自己的程序中生成图片、创建视频、上传参考图片和查询视频结果。使用流星 API 主站创建的 API Key，生成费用计入该 Key 所属账户。

主站：[流星 API](https://api.meteor21c.fun/) · 工作台：[流星创作台](https://api.meteor21c.fun/studio) · [计价标准](https://api.meteor21c.fun/studio/pricing)

## 目录

1. [快速接入](#1-快速接入)
2. [客户端配置](#2-客户端配置)
3. [获取工具和模型](#3-获取工具和模型)
4. [请求与返回格式](#4-请求与返回格式)
5. [生成和编辑图片](#5-生成和编辑图片)
6. [上传本地参考图片](#6-上传本地参考图片)
7. [创建视频](#7-创建视频)
8. [查询与下载视频](#8-查询与下载视频)
9. [查询价格与费用参考](#9-查询价格与费用参考)
10. [大小限制与有效期](#10-大小限制与有效期)
11. [错误处理](#11-错误处理)
12. [常见问题](#12-常见问题)

## 1. 快速接入

### 准备 API Key

登录主站，在「API 密钥」中创建或选择一把允许使用目标模型的 Key。确认所属分组开放了图片或视频能力，并且账户余额、Key 额度和有效期可用。

所有 MCP 请求使用：

```http
Authorization: Bearer YOUR_METEOR_API_KEY
Content-Type: application/json
Accept: application/json, text/event-stream
```

`YOUR_METEOR_API_KEY` 替换为完整的本站 API Key。不要填写网站登录密码、浏览器登录 Token 或其他平台的 Key。

### 服务地址

| 用途 | URL | 工具 |
| --- | --- | --- |
| 图片和视频，推荐 | `https://api.meteor21c.fun/mcp` | 全部四个工具 |
| 仅图片 | `https://api.meteor21c.fun/mcp/image` | `create_image`、`create_material_upload` |
| 仅视频 | `https://api.meteor21c.fun/mcp/video` | `create_video`、`get_video`、`create_material_upload` |

连接类型选择 **HTTP / Streamable HTTP**。请求通过 HTTP POST 发送 JSON-RPC；用浏览器直接打开 MCP 地址出现 405，属于请求方法不匹配。

### 第一次使用

1. 配置服务地址和 API Key。
2. 用 `tools/list` 获取工具定义。
3. 用同一把 Key 请求 `GET /v1/models`，获取可选模型 ID。
4. 明确模型和参数后调用 `create_image` 或 `create_video`。
5. 视频创建后保存 `task_id`，持续查询同一任务，完成后下载。

**生成会扣费。查看工具、模型、价格和任务状态不会另建生成任务。**

## 2. 客户端配置

### 通用 HTTP MCP 客户端

在客户端的 MCP 设置中填写：

| 配置项 | 值 |
| --- | --- |
| 名称 | `meteor-media` |
| 类型 | HTTP / Streamable HTTP |
| 地址 | `https://api.meteor21c.fun/mcp` |
| 请求头名称 | `Authorization` |
| 请求头值 | `Bearer YOUR_METEOR_API_KEY` |
| 工具超时 | 建议至少 360 秒，图片生成可能需要等待 |

支持 `mcpServers` 配置的客户端可参考以下结构；字段名称以该客户端的设置为准：

```json
{
  "mcpServers": {
    "meteor-media": {
      "url": "https://api.meteor21c.fun/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_METEOR_API_KEY"
      }
    }
  }
}
```

### Codex

在 `~/.codex/config.toml` 中新增一个服务：

```toml
[mcp_servers.meteor_media]
url = "https://api.meteor21c.fun/mcp"
bearer_token_env_var = "METEOR_API_KEY"
startup_timeout_sec = 30
tool_timeout_sec = 360
```

启动 Codex 的环境中需要存在 `METEOR_API_KEY`。例如在同一终端设置变量后启动 Codex：

```sh
export METEOR_API_KEY='YOUR_METEOR_API_KEY'
codex
```

Windows PowerShell：

```powershell
$env:METEOR_API_KEY = 'YOUR_METEOR_API_KEY'
codex
```

图形客户端需要能读取该环境变量。也可以省略 `bearer_token_env_var`，改为在同一配置节设置静态请求头：

```toml
http_headers = { Authorization = "Bearer YOUR_METEOR_API_KEY" }
```

两种鉴权配置选择一种即可。配置中含有真实 Key 时，请保存在本机，分享配置前删除 Key。

检查已配置服务可运行 `codex mcp list`；修改后重新打开对话，再让智能体使用流星媒体工具。

配置字段参考：[OpenAI 官方 MCP 文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)。

## 3. 获取工具和模型

### 获取工具及完整参数

```sh
curl -sS 'https://api.meteor21c.fun/mcp' \
  -H "Authorization: Bearer $METEOR_API_KEY" \
  -H 'Content-Type: application/json' \
  --data-binary '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

返回 `result.tools`。每项包含 `name`、`description` 和 `inputSchema`；其中 `required` 表示必填项，`enum` 表示枚举值。

| 工具 | 用途 | 执行方式 |
| --- | --- | --- |
| `create_image` | 文生图、使用已上传图片编辑或参考生图 | 同步等待图片结果 |
| `create_material_upload` | 申请本地参考图片的临时上传地址 | 返回地址后，客户端执行 PUT 上传 |
| `create_video` | 创建视频生成任务 | 异步，返回任务 ID |
| `get_video` | 查询视频进度、结果和失败原因 | 查询已有任务 |

### 获取可用模型

```sh
curl -sS 'https://api.meteor21c.fun/v1/models' \
  -H "Authorization: Bearer $METEOR_API_KEY"
```

示例结构：

```json
{
  "object": "list",
  "data": [
    {"id": "doubao-seedance-2.0", "object": "model"},
    {"id": "doubao-seedance-2.0-mini", "object": "model"}
  ]
}
```

调用时填写返回的 **完整 `id`**，不要自行改写为模型显示名称。不同 Key 所属分组不同，模型列表也可能不同。

当前四个 MCP 工具中没有 `list_models`；模型通过上述 HTTP 接口获取。智能体没有额外 HTTP 请求工具时，可以由用户查询后提供模型 ID。

**模型列表代表 Key 的模型范围；还需要确认所选 MCP 工具支持该模型和参数。** 图片工具使用 Images 兼容接口；仅支持其他原生协议的模型不能仅凭出现在列表中就用于 `create_image`。

视频的分辨率和计费场景可用第 9 节的目录接口查询。目录用于确认分辨率、声音和输入视频对应的计费场景，不包含全部素材格式与时长约束。

## 4. 请求与返回格式

### 初始化

常规 MCP 客户端会自动初始化。自行编写客户端时发送：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": {"name": "my-meteor-client", "version": "1.0.0"}
  }
}
```

服务当前返回协议版本 `2025-06-18`。随后可发送不含 `id` 的 `notifications/initialized` 通知；通知返回 HTTP 202，无 JSON 响应体。

### 调用工具

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "工具名称",
    "arguments": {}
  }
}
```

一次请求发送一个 JSON-RPC 对象。只传工具声明的参数；这里不采用 OpenAI `messages` 或 `input` 的请求结构。

### 判断成功

按以下顺序检查：

1. HTTP 状态是否正常。
2. 是否存在顶层 `error`。
3. `result.isError` 是否为 `true`。
4. 工具数据中的 `status` 是否已到成功或失败终态。

**HTTP 200 本身不能代表图片或视频生成成功。**

优先读取 `result.structuredContent`。如果客户端只保留文本，可读取 `result.content` 中 `type: "text"` 的内容；视频工具的文本内容也是 JSON。

## 5. 生成和编辑图片

工具：`create_image`，入口 `/mcp` 或 `/mcp/image`。

### 参数

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `model` | string | 是 | 当前 Key 可用、支持 Images 接口的完整图片模型 ID |
| `prompt` | string | 是 | 图片描述或编辑要求，不能为空 |
| `n` | integer | 否 | 生成张数，1–4，默认 1 |
| `size` | string | 否 | 模型支持的尺寸，例如 `1024x1024`、`1536x1024`；不传时使用渠道默认值 |
| `quality` | string | 否 | 模型支持的质量档位；例如部分模型使用 `auto`、`low`、`medium`、`high`，并非所有模型通用 |
| `output_format` | string | 否 | 填 `gif` 生成动图，仅限当前分组已开放的 Image 2.6 系列模型；省略则生成普通图片 |
| `reference_material_ids` | string[] | 否 | 已完成上传的参考图片 ID，最多 4 个 |
| `force_new` | boolean | 否 | 兼容保留字段，通常省略；当前不提供去重或防重复计费能力 |

`quality` 和分辨率是不同概念。`low/medium/high` 不代表 `1K/2K/4K`；需要更高分辨率时，按目标模型的尺寸规则填写 `size`。

GIF 动图每次仅生成 1 张，尺寸为 `auto`，暂不支持参考图、指定帧数或帧时长。SVG 暂未开放。

使用相同 API Key 请求 `GET https://api.meteor21c.fun/v1/images/capabilities?model=gpt-image-2.6`，返回的 `output_formats` 含 `gif` 时可用。此查询不生成图片、不扣费；工具也会在提交前检查。

查询时填写实际选用的模型名，例如 `gpt-image-2.6-flare`、`gpt-image-2.6-sunburst` 或 `gpt-image-2.6-adobe`。能力按当前 Key 所属分组判断，GIF 请求自动交给该分组内支持此模型和格式的上游，无需指定上游渠道。普通图片的模型选择和计费规则不变。

动图示例参数：

```json
{"model":"gpt-image-2.6","prompt":"一个蓝色圆球轻轻跳动，循环动画","n":1,"size":"auto","output_format":"gif"}
```

预览和下载保留 GIF 原文件。若实际返回普通图片，会显示提示并保留已生成的原文件，结果中的 `format_matched` 为 `false`、`warnings` 说明实际格式。请勿因格式不符自动重试。

### 文生图示例

以下请求会提交生成并产生费用；先将模型替换为自己 Key 可用的模型。

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "tools/call",
  "params": {
    "name": "create_image",
    "arguments": {
      "model": "gpt-image-2",
      "prompt": "一张简洁的春日咖啡店插画，暖色调，无文字",
      "n": 1,
      "size": "1024x1024"
    }
  }
}
```

### 图片编辑或参考生图

先按第 6 节上传图片，再在相同工具中加入：

```json
{
  "model": "gpt-image-2",
  "prompt": "保留参考图中的杯子，将背景改为浅蓝色桌面",
  "n": 1,
  "reference_material_ids": ["上传成功后返回的material_id"]
}
```

提供参考图片后，请求会使用图片编辑接口；所选模型需要同时支持图片编辑。

### 读取图片结果

成功结果包含：

| 位置 | 含义 |
| --- | --- |
| `result.content[]` 中 `type: "image"` | 原生图片块，含 `mimeType` 和 Base64 `data` |
| `result.content[]` 中 `type: "resource_link"` | 可用时返回的临时下载地址 |
| `result.structuredContent.status` | 成功时为 `SUCCESS` |
| `result.structuredContent.count` | 实际交付的图片张数 |
| `result.structuredContent.images[]` | 每张图片的索引、类型，以及可选的 `url`、`expires_at` |

下载地址可能缺失，但原生图片块仍可保存。客户端不显示图片时，可解码原生图片块，或使用返回的下载地址；不要为了解决显示问题再次提交生成。

## 6. 上传本地参考图片

工具：`create_material_upload`，三个 MCP 入口均可使用。

### 第一步：申请上传地址

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `file_name` | string | 是 | 文件名，建议带正确扩展名，例如 `reference.png` |
| `content_type` | string | 是 | `image/jpeg`、`image/png` 或 `image/webp` |
| `size_bytes` | integer | 是 | 原文件的准确字节数，1–10,485,760，即最大 10 MiB |

示例中的 `123456` 需要替换为原图的实际字节数：

```json
{
  "jsonrpc": "2.0",
  "id": 20,
  "method": "tools/call",
  "params": {
    "name": "create_material_upload",
    "arguments": {
      "file_name": "reference.png",
      "content_type": "image/png",
      "size_bytes": 123456
    }
  }
}
```

读取返回的 `result.structuredContent`：

```json
{
  "material_id": "MATERIAL_ID",
  "upload_url": "https://api.meteor21c.fun/mcp/assets/UPLOAD_ID?exp=...&op=upload&sig=...",
  "method": "PUT",
  "headers": {"Content-Type": "image/png"},
  "expires_at": 1800000000
}
```

### 第二步：上传原始文件字节

把 `UPLOAD_URL` 替换为完整的 `upload_url`，执行：

```sh
curl --fail-with-body -sS -X PUT "$UPLOAD_URL" \
  -H 'Content-Type: image/png' \
  --data-binary '@reference.png'
```

此处上传的是原始文件字节，**不使用 multipart、JSON 或 Base64**。按返回的 `headers` 设置请求头；签名上传地址不需要额外填写 Bearer Key。

上传成功返回 HTTP 200，响应体为空。成功后才可把 `material_id` 传给生成工具。

### 第三步：引用素材

- 图片：`reference_material_ids`。
- 视频参考图：`reference_material_ids`。
- 视频首帧或尾帧：`start_material_id`、`end_material_id`。

上传和引用必须使用同一把 API Key。上传地址约 15 分钟内有效，一次成功后不能重复 PUT；过期或上传内容错误时重新申请地址。

素材上传工具当前仅接收上述三种图片。`material_id` 不能替换为本地路径、`file://` 地址或网页素材的 `server:...` 标识。

## 7. 创建视频

工具：`create_video`，入口 `/mcp` 或 `/mcp/video`。每次创建一个异步视频任务。

### 参数

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `model` | string | 否 | 建议明确填写；默认 `doubao-seedance-2.0-fast`，仍须该 Key 有权限 |
| `prompt` | string | 是 | 视频描述，不能为空，最多 1300 个字符；表情等可能占多个字符计数 |
| `duration` | integer | 否 | 秒数，默认 5；合法区间见下表 |
| `resolution` | string | 否 | 工具枚举为 `480p`、`720p`、`1080p`、`4K`；只选目标模型目录当前支持的档位 |
| `aspect_ratio` | string | 否 | `16:9`、`9:16`、`1:1`、`4:3`、`3:4`、`21:9`；模型实际支持范围可能更小 |
| `audio` | boolean | 否 | 是否生成声音；豆包默认开启，可灵默认关闭；模型不支持的组合会失败 |
| `reference_images` | string[] | 否 | 公网参考素材地址；可用 `image:`、`video:`、`audio:` 前缀标注类型，需模型支持 |
| `reference_material_ids` | string[] | 否 | 已通过 MCP 上传的参考图片 ID；与 `reference_images` 合计最多 9 项 |
| `start_image_url` | string | 否 | 公网首帧图片 URL；可灵 Omni 首尾帧见下文专用格式 |
| `end_image_url` | string | 否 | 公网尾帧图片 URL；可灵 Omni 首尾帧见下文专用格式 |
| `start_material_id` | string | 否 | 已上传的首帧图片 ID；填写时优先于 `start_image_url` |
| `end_material_id` | string | 否 | 已上传的尾帧图片 ID；填写时优先于 `end_image_url` |
| `mode` | string | 否 | 兼容字段：`text_with_reference`、`start_end_frame`、`std`、`pro`、`4k`；当前模型通常省略，首尾帧和清晰度分别使用对应字段控制 |

未传清晰度时，豆包默认 `720p`、可灵默认 `1080p`；未传画幅时，豆包默认 `9:16`、可灵默认 `16:9`。建议明确填写这两个参数。

### 工具识别的模型和时长

| 模型 ID | `duration` 区间 |
| --- | --- |
| `doubao-seedance-2.0` | 4–15 秒 |
| `doubao-seedance-2.0-fast` | 4–15 秒 |
| `doubao-seedance-2.0-mini` | 4–15 秒 |
| `doubao-seedance-2.5` | 4–30 秒 |
| `doubao-seedance-1.5-pro` | 4–12 秒，须当前模型列表和目录实际开放 |
| `kling-v3` | 3–15 秒 |
| `kling-v3-omni` | 3–15 秒 |

以上是 MCP 工具接受的模型和时长，**不代表每把 Key 都能使用，也不代表每个模型支持全部清晰度与参考素材类型**。

`MiniMax-H3` 和 `tencent-mps-superres` 当前尚不能通过 `create_video` 创建。使用这两项能力请进入流星创作台的视频页面。

### 文生视频示例

此调用会创建付费任务：

```json
{
  "jsonrpc": "2.0",
  "id": 30,
  "method": "tools/call",
  "params": {
    "name": "create_video",
    "arguments": {
      "model": "doubao-seedance-2.0-mini",
      "prompt": "海边清晨，镜头缓慢向前移动，海浪轻拍岸边，画面稳定自然",
      "duration": 5,
      "resolution": "720p",
      "aspect_ratio": "16:9",
      "audio": false
    }
  }
}
```

### 首尾帧与多素材参考

建议每次选择一种输入方式：文生视频、首尾帧，或多素材参考。不要假定所有模型都允许混合首尾帧与参考素材。

豆包首尾帧可使用以下参数；尾帧与首帧配套使用：

```json
{
  "start_material_id": "FIRST_FRAME_MATERIAL_ID",
  "end_material_id": "LAST_FRAME_MATERIAL_ID"
}
```

已有公开图片 URL 时，也可使用 `start_image_url`、`end_image_url`。

多素材参考使用 `reference_images`；不透明链接建议明确标注类型：

```json
{
  "reference_images": [
    "image:https://your-public-host.example/character.png",
    "video:https://your-public-host.example/motion.mp4",
    "audio:https://your-public-host.example/voice.wav"
  ]
}
```

示例域名需要替换为可公开访问的真实地址。远程参考不接受 localhost、内网地址、需要网页登录的页面或本地文件路径。上传音视频目前需要自行准备公开 URL，并确认目标模型支持相应的输入类型。

部分重要限制：

- 豆包参考素材可能先进入素材处理与审核阶段，继续查询原任务即可。
- `kling-v3` 主要使用首尾帧；多素材能力请使用支持参考素材的具体模型。
- 可灵 Omni 首尾帧通过 `reference_images` 中的 `start:https://...`、`end:https://...` 传入；不要改用该模型当前未映射的独立首尾帧字段。
- 可灵 Omni 引用视频时必须设置 `audio: false`。
- 9 项是工具总额度；目标模型的参考素材限制可能更小，不能为了达到额度向不支持的模型追加素材。

### 创建结果

成功创建后读取：

```json
{
  "task_id": "TASK_ID_FROM_CREATE",
  "id": "TASK_ID_FROM_CREATE",
  "status": "PENDING",
  "model": "doubao-seedance-2.0-mini"
}
```

该对象位于 `result.structuredContent`。响应还可能包含 `stage`、`asset_progress`、`billing` 和 `raw`。`billing` 是创建时的费用信息，不应作为最终实际扣费。

立即保存 `task_id`，后续通过 `get_video` 查询。不要通过再次调用 `create_video` 查询进度。

## 8. 查询与下载视频

工具：`get_video`。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 创建任务返回的原始任务 ID，使用同一把 API Key 查询 |

```json
{
  "jsonrpc": "2.0",
  "id": 31,
  "method": "tools/call",
  "params": {
    "name": "get_video",
    "arguments": {"task_id": "TASK_ID_FROM_CREATE"}
  }
}
```

### 状态

| `status` | 含义 | 下一步 |
| --- | --- | --- |
| `PENDING` | 已创建，等待后续处理 | 保存 ID，查询同一任务 |
| `IN_PROGRESS` | 正在处理或生成 | 建议间隔 5–10 秒查询 |
| `SUCCESS` | 视频已生成 | 读取 `data.result_url` 并下载 |
| `FAILURE` | 生成失败 | 读取 `data.fail_reason`，停止轮询并核对费用记录 |

成功示例，省略了其他可选字段：

```json
{
  "task_id": "TASK_ID_FROM_CREATE",
  "status": "SUCCESS",
  "data": {
    "task_id": "TASK_ID_FROM_CREATE",
    "status": "SUCCESS",
    "result_url": "https://api.meteor21c.fun/mcp/assets/RESULT_ID?exp=...&op=read&sig=...",
    "video_url": "https://api.meteor21c.fun/mcp/assets/RESULT_ID?exp=...&op=read&sig=..."
  }
}
```

### 下载

使用返回的完整 `data.result_url`，不要删除查询参数：

```sh
curl --fail-with-body -sS -L "$RESULT_URL" -o result.mp4
```

本站 `/mcp/assets/` 签名结果地址可在有效期内直接访问，支持 Range。若返回的是受保护的 `/v1/...` 内容地址，则下载时还需要携带原 API Key 的 Bearer 请求头。

临时视频签名链接约 30 分钟有效。过期时可用原 Key 再次调用 `get_video`，在文件仍可用时获取新链接；续签不会重新创建视频，也不能恢复已过期的源文件。生成完成后尽快保存到本地。

## 9. 查询价格与费用参考

价格会随模型、分组、参数和当前可用场景变化。下列接口使用同一把 API Key，查询本身不会提交生成。

### 查询视频目录和场景

```sh
curl -sS --get 'https://api.meteor21c.fun/v1/videos/pricing' \
  -H "Authorization: Bearer $METEOR_API_KEY" \
  --data-urlencode 'catalog=true' \
  --data-urlencode 'model=doubao-seedance-2.0'
```

重点读取：

| 字段 | 说明 |
| --- | --- |
| `available_models` | 当前目录中存在的模型；再与 Key 的模型范围、MCP 工具支持范围取交集 |
| `models[]` | 各模型的目录项 |
| `resolutions` | 当前所查模型的分辨率集合 |
| `billing_modes` | 计费单位，例如 `token`、`second` |
| `scenarios[]` | 按输入视频、声音等条件区分的计费场景 |
| `scenarios[].audio` | 布尔值时匹配相同声音选项；为 `null` 时该价格场景不按此项区分 |
| `scenarios[].input_video` | 布尔值时匹配是否包含参考视频；`null` 表示该场景未按此项区分 |

当前模型的分辨率集合可能覆盖多个场景。应先匹配 `audio`、`input_video`，再选匹配场景中的分辨率；不要只看顶层集合。

### 查询指定参数的预扣与参考费

```sh
curl -sS --get 'https://api.meteor21c.fun/v1/videos/pricing' \
  -H "Authorization: Bearer $METEOR_API_KEY" \
  --data-urlencode 'model=doubao-seedance-2.0' \
  --data-urlencode 'resolution=720p' \
  --data-urlencode 'duration=5' \
  --data-urlencode 'aspect_ratio=16:9' \
  --data-urlencode 'audio=true' \
  --data-urlencode 'input_video=false'
```

`audio`、`input_video` 使用 `true/false`。查询参数应与实际提交一致；`input_video` 表示有无参考视频，不代表请求输出视频。

| 返回字段 | 用户含义 |
| --- | --- |
| `billing_mode` | `token` 为 Token 计费，`second` 为按秒计费 |
| `currency` | 价格单位对应的币种 |
| `sale_per_million` | 本站每百万 Token 单价，仅适用于 Token 场景 |
| `sale_per_second` | 本站每秒单价，仅适用于按秒场景 |
| `official_per_million` / `official_per_second` | 对应官方标准单价 |
| `sale_rate_to_official` | 本站单价 / 官方单价；例如 `0.345` 表示官方价格的 34.5%，即 **3.45 折** |
| `precharge_amount` | 提交时先预扣的余额数值，目前最高 5 |
| `estimate_source` | `personal` 为本人历史参考，`default` 为模型默认预扣，`exact` 为可按参数估算 |
| `estimated_amount` | 可获得时返回的参考总费用；缺失时表示本次没有参考价 |
| `estimate_low` / `estimate_high` | 历史参考区间，可选 |
| `estimate_samples` | 参考样本数 |

**预扣最高 5，不等于最终费用最多 5。** Token 场景完成后按实际用量结算，费用低于预扣时退差额，高于预扣时补扣。没有参考价时仍可按模型默认预扣提交；参考价仅用于提示。

Token 场景的基础费用为：

```text
实际输出 Token / 1,000,000 × 本站每百万 Token 单价
```

按秒场景的基础费用为实际计费时长 × 本站每秒单价；存在明确附加计费项时还需加上相应费用。最终以账户使用记录和任务结算结果为准。

本站当前余额采用数值 1:1 结算口径：计价金额的数值对应同额账户余额，不进行实时人民币/美元汇率换算。

### 查询本人历史费用参数

```sh
curl -sS --get 'https://api.meteor21c.fun/v1/videos/pricing' \
  -H "Authorization: Bearer $METEOR_API_KEY" \
  --data-urlencode 'model=doubao-seedance-2.0' \
  --data-urlencode 'history=true'
```

返回 `history` 数组，最多包含本人近 90 天最近 20 条已结算记录的时长、清晰度、Token 和费用等参数；不返回提示词或素材内容。没有记录时返回空数组。

图片费用请查看创作台「计价标准」，结合当前 Key 的分组、模型、尺寸和质量档位确认。以上 `/v1/videos/pricing` 接口用于视频。

## 10. 大小限制与有效期

| 项目 | 当前限制 |
| --- | --- |
| 一次图片生成 | 最多 4 张 |
| 图片工具参考图 | 最多 4 张 |
| MCP 本地上传 | JPEG / PNG / WebP，单张最多 10 MiB |
| 视频工具参考列表 | URL 与 `reference_material_ids` 合计最多 9 项；还需遵守模型限制 |
| 视频工具任务数 | 一次一个 |
| 参考图片上传地址 | 约 15 分钟，上传成功后不可复用 |
| MCP 临时参考图片 | 约 24 小时；请在有效期内提交引用 |
| 图片结果下载地址 | 以 `images[].expires_at` 为准，通常约 24 小时 |
| 视频签名下载链接 | 约 30 分钟；文件仍有效时可通过 `get_video` 重新获取 |

`expires_at` 是 Unix 秒级时间戳。所有临时素材均有定期清理机制；请把生成结果及时保存到本地。网页画布的默认七天保留规则不适用于这些 MCP 临时上传和签名地址。

图片生成等待上限约 5 分钟。等待超时后，任务可能仍已生成或产生费用；先核对使用记录，不要由程序自动重新生成。

## 11. 错误处理

### 三类错误

HTTP 层错误：

```json
{"error":{"type":"authentication_error","message":"Bearer token is required"}}
```

JSON-RPC 协议错误：

```json
{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"tool is not available on this MCP endpoint"}}
```

工具执行错误，可能仍是 HTTP 200：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "isError": true,
    "content": [{"type": "text", "text": "model and prompt are required"}]
  }
}
```

### 常见情况

| 现象 | 处理方式 |
| --- | --- |
| 401 或 Key 不可用 | 检查完整 Key、有效期、所属分组、额度和账户状态 |
| 能列工具，生成时无模型权限 | 用相同 Key 请求 `/v1/models`；工具发现成功不等于目标模型已授权 |
| `tool is not available...` | 核对连接的是图片入口、视频入口还是统一入口 |
| `unsupported video model` | 核对第 7 节的 MCP 视频支持范围及模型 ID 大小写 |
| `size_bytes`、格式或文件大小错误 | 读取实际字节数，核对扩展名、MIME 与原始文件类型 |
| `material_id` 无效、过期或属于其他用户 | 使用原 Key，确认 PUT 成功；过期后重新上传 |
| 引用 URL 不可用 | 使用能公开直接访问的文件 URL，核对权限和有效期 |
| 服务繁忙，明确提示未提交和未扣费 | 稍后手动重试，避免同时提交大量任务 |
| 请求超时、网络断开、返回内容不完整 | 先检查原任务或使用记录，确认状态后再决定是否重试 |
| 图片已成功但不显示 | 使用原生图片块或下载链接，保存现有结果 |
| 视频临时链接过期 | 用原 Key 查询原 `task_id`，尝试获取新链接 |
| 视频 `FAILURE` | 保存 `task_id` 和 `data.fail_reason`，核对任务与费用记录 |

反馈问题时提供时间、工具名、模型 ID、任务 ID 和错误信息即可。不要发送 API Key 或完整签名链接。

## 12. 常见问题

### MCP 会自动在我的画布里创建节点吗？

本文三个远程 MCP 地址用于直接生成和获取媒体。它们不读写当前网页画布。

如需让智能体创建节点、连线和整理画布，使用画布页面内的「连接本地 Agent」流程。该流程使用本机 Canvas Agent，工具名称包括 `get_generation_config`、`generate_image`、`generate_video` 等；连接 Token 与本站 API Key 用途不同。

### 为什么客户端说无法读取我的本地图片？

`create_material_upload` 负责申请上传地址，客户端仍需有读取本地文件和执行 HTTP PUT 的能力。只把电脑路径写入提示词，远程 MCP 无法读取该文件。

### 能把网页里的素材 ID 直接传进 MCP 吗？

`material_id` 应使用本 MCP 上传流程返回的 ID。网页素材 ID、画布节点 ID、`server:...` 和本地文件名不能直接替代它。

### 每个工具都需要不同 Key 吗？

可以使用同一把同时有图片和视频权限的 Key。如果权限分属不同分组，则为对应能力分别配置 Key，并在各自流程内保持一致。

### 创建视频超时后，可以立即再创建一次吗？

先确认原任务或使用记录。再次调用创建工具会提交新的生成请求，可能再次产生费用；当前 MCP 没有供用户设置的幂等参数。

### `raw` 字段可以作为稳定接口使用吗？

请优先使用本文列出的 `task_id`、`status`、`data.result_url` 等字段。`raw` 是补充信息，结构可能变化。

---

完整工具参数见同目录的 [mcp-tools.json](mcp-tools.json)，可复制请求见 [request-examples.http](request-examples.http)。工具升级后，请再次用 `tools/list` 获取当前定义。

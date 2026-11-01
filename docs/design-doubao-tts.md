# 豆包 TTS 集成设计（edgetts-speechd）

## 1. 背景与实测结论

豆包浏览器插件（`capohkkfagimodmlpnahjoijgoocdjhd` v1.38.0）包含两套 TTS 协议：

| 协议 | 端点 | 状态 |
|------|------|------|
| 旧版 JSON | `wss://ws-samantha.doubao.com/samantha/audio/tts` | 已弃用，无 Cookie 与带 Cookie 均返回 `710012001 登录已过期` |
| 新版 protobuf | `wss://frontier-audio-web-ws.doubao.com/api/v2/sami/voicegenie` | 可用，需豆包登录态 |

实测证据：

- 无 Cookie：`SessionFailed status=40200001 text="session auth, userID or appID is empty"`
- 带 Edge 中已登录的 27 条 `doubao.com` Cookie：
  `TaskStarted → SessionStarted → TTSSentenceStart → TTSResponse(data=1455/2075/... 字节，头部 "OggS") → TTSSentenceEnd → TTSEnded`

## 2. 协议实现

### 2.1 连接参数

```
wss://frontier-audio-web-ws.doubao.com/api/v2/sami/voicegenie
  ?api_app_key=GOqQpfo1fO7slHv8
  &namespace=VoiceGenie
  &version_code=20800
  &language=zh
  &device_platform=web
  &pkg_type=release_version
  &pc_version=3.9.0
  &region=CN
  &sys_region=CN
  &samantha_web=1
  &use-olympus-account=1
  &aid=586864
  &real_aid=586864
  &browser_language=zh-CN
  &is_new_user=0
  &web_id=<随机>
  &tea_uuid=<随机>
  &web_tab_id=<随机>
  &device_id=<随机>
```

请求头需要：
- `Origin: chrome-extension://capohkkfagimodmlpnahjoijgoocdjhd`
- `Cookie: <doubao.com 登录态>`
- 桌面 Chrome UA

### 2.2 protobuf 消息

`WebSocketRequest`：

| 字段 | 编号 | 类型 |
|------|------|------|
| token | 1 | string |
| appkey | 2 | string |
| namespace | 3 | string |
| version | 4 | string |
| event | 5 | string |
| payload | 6 | string |
| data | 7 | bytes |
| task_id | 8 | string |
| seq_id | 9 | uint64 |

`WebSocketResponse`：

| 字段 | 编号 | 类型 |
|------|------|------|
| task_id | 1 | string |
| message_id | 2 | string |
| namespace | 3 | string |
| event | 4 | string |
| status_code | 5 | int32 |
| status_text | 6 | string |
| payload | 7 | string |
| data | 8 | bytes |
| seq_id | 9 | uint64 |

使用手写 varint 编码，不引入 protobuf 依赖。

### 2.3 交互流程

1. `StartTask`（appkey + namespace）
2. 等待 `TaskStarted`，记录 `task_id`
3. `StartSession`，payload 为 JSON：

```json
{
  "business": 1,
  "conversation_id": "",
  "chat": {
    "bot_id": "", "conversation_id": "", "question_id": "", "message_id": "",
    "new_conversation": false, "chat_next": null,
    "enable_toast_reading": "true",
    "extra": {"chat_next": null}
  },
  "request_type": 4,
  "enable_text_reading": true,
  "interrupt_type": 0,
  "query_mode": 2,
  "tts": {
    "speaker": "<说话人>",
    "audio_config": {"bit_rate": 32000, "format": "ogg_opus", "sample_rate": 24000},
    "extra": {"chat_next": null, "post_process": {"pitch": 0, "speech_rate": 1}}
  },
  "extra": {
    "enable_toast_reading": "true", "chat_next": null,
    "extra": "{\"post_process\":{\"pitch\":0,\"speech_rate\":1},\"music_ext_for_tts\":\"\",\"network_level\":8,\"chat_next\":null}"
  }
}
```

4. 等待 `SessionStarted`
5. `BidirectionalTTS`，payload 为 `{"text": "<待合成文本>"}`
6. `EndTTS`
7. 收集 `TTSResponse` 的 `data`（Ogg Opus 分片），直到 `TTSEnded`
8. `SessionFailed` / `TaskFailed` 视为错误

## 3. Cookie 获取策略（A+C 组合）

优先级：

1. 环境变量 `DOUBAO_COOKIE`（用户显式指定时最高优先）
2. 本地持久化缓存 `~/.config/edgetts-speechd/doubao.json` 的 `cookie` 字段
3. 缓存不存在或已失效时，自动从 Edge 提取，并写回缓存：
   - 复制 `~/.config/microsoft-edge/Default/Cookies`、`Local State` 到临时目录
   - 启动临时 headless Edge：`--user-data-dir=<tmp> --remote-debugging-port=<port> --headless=new`
   - 通过 CDP `Storage.getCookies` 读取，过滤 `doubao.com`
4. 全部失败：明确报错，提示用户登录 Edge 中的豆包或设置 `DOUBAO_COOKIE`

失效判定以服务端为准：

- 缓存不设固定过期时间，始终优先复用。
- 服务端返回 `40200001`（`session auth`）或连接被拒时，视为 Cookie 失效：
  1. 删除旧缓存；
  2. 自动重新从 Edge 提取；
  3. 用新 Cookie 重试一次；
  4. 仍失败则报错并保留诊断信息。

缓存文件权限为 `0600`，只保存 Cookie 字符串与更新时间。

自动提取仅在 Linux 启用；其他平台直接提示使用手动 Cookie。

实现约束：启动 headless Edge 时不能添加 `--password-store=basic`。实测该参数会让 Edge 使用基础密码库，无法解密 KWallet 保存的 Cookie，并且 Edge 会把无法解密的条目从副本数据库中删除（副本 Cookies 表从 27 条 doubao 记录变为 0 条）。去掉该参数后，同一份副本可正常读出 27 条 Cookie。

## 4. 代码结构

| 文件 | 职责 |
|------|------|
| `tts/doubao.go` | `DoubaoTTS` 实现 `Core` 接口，WebSocket 流程 |
| `tts/doubaoProto.go` | protobuf varint 编解码 |
| `tts/doubaoCookie.go` | Cookie 读取、缓存、校验 |
| `tts/doubaoEdgeCookie.go` | Edge 配置复制 + headless Edge + CDP 提取 |
| `tts/doubaoConfig.go` | 豆包配置结构与校验 |

## 5. CLI 设计

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-engine` | `edge` | `edge` 或 `doubao` |
| `-dv` | `zh_female_wenroutaozi_uranus_bigtts` | 豆包说话人 |
| `-doubao-cookie` | 空 | 直接指定 Cookie |
| `-doubao-refresh` | false | 强制重新从 Edge 提取 Cookie |

`-v/-r/-vol/-p` 仍用于 Edge TTS；豆包模式下：

- `-r` 支持百分比写法（`+10%` → `1 + 10/100 = 1.1`）与直接倍数写法（`1.1`）
- `-p` 支持整数（`-2`）与带单位写法（`-50Hz`），单位只剥离不做换算
- `-vol` 忽略并提示

## 6. 音频输出

- 豆包返回 Ogg Opus。
- 输出路径以 `.mp3` 结尾（或默认路径）时，调用 `ffmpeg` 转码为 MP3；`ffmpeg` 不可用时保留 `.ogg` 原始数据并提示。
- 输出路径以 `.ogg` / `.opus` 结尾时直接写入原始数据。
- `ffprobe` 查看 Ogg Opus 时会显示 48000 Hz，这是 Opus 解码输出的固定采样率；请求侧采样率为 24000。

## 7. 错误处理

- Cookie 缺失或过期（`40200001` / `SessionFailed`）：提示“豆包登录态失效，请重新登录 Edge 中的豆包，或设置 DOUBAO_COOKIE”。
- 服务端返回非 `20000000` 状态码：透出 `status_code` 与 `status_text`。
- 读取超时：默认 30 秒，超时返回明确错误。

## 8. 测试

单元测试（`go test ./...`）：

- `tts/doubaoProto_test.go`：请求字段编号精确字节、响应解码、未知字段跳过、长度越界报错。
- `tts/doubaoCookie_test.go`：缓存落盘权限 0600、优先级（显式 > 环境变量 > 缓存）、清空幂等。
- `tts/doubaoEdgeCookie_test.go`：Cookie 头过滤非豆包域、同名去重、值原样拼接。
- `tts/doubao_test.go`：配置校验、StartSession JSON 结构、连接参数、登录态失效识别、输出格式判断。
- `main_test.go`：豆包语速与音调参数解析。

真实调用为集成验证，需要本机已登录 Edge：

```bash
./edgetts -engine doubao -o /tmp/e2e.ogg "测试豆包语音输出。"
ffprobe -hide_banner /tmp/e2e.ogg
```

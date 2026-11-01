# edgetts-speechd

use the edgetts for the speech-dispatcher backend

实现原理和 edge-tts 相同，通过 edge tts 的地址通信获取音频数据，自带音频播放不依赖 mpv 等第三方播放器

配合 speech-dispatcher 的配置文件，实现了 linux 下默认 tts 调用 edgetts,foliate 软件能听书，orca 也能使用 edgetts 语音

## 功能

- Edge TTS（默认）：微软在线语音，支持全部 `*-Neural` 说话人
- 豆包 TTS：豆包 Web 端 VoiceGenie 协议，返回 Ogg Opus 音频，可转码为 MP3
- 豆包登录 Cookie 本地持久化，失效时自动从 Edge 重新提取

## 安装

```bash
go build -o edgetts .
```

## 用法

```bash
edgetts [选项] <文本>
```

### Edge TTS（默认）

```bash
# 直接播放
./edgetts "你好，这是测试文本"

# 指定说话人并只生成文件
./edgetts -v zh-CN-XiaoxiaoNeural -o output.mp3 "你好"

# 列出全部可用语音
./edgetts -l
```

### 豆包 TTS

```bash
# 使用默认说话人 zh_female_wenroutaozi_uranus_bigtts
./edgetts -engine doubao -o output.mp3 "你好，这是豆包语音"

# 指定说话人、语速（+10% 或 1.1）与音调
./edgetts -engine doubao -dv zh_female_wenroutaozi_uranus_bigtts -r +10% -p -2 -o output.ogg "语速与音调"

# 直接指定 Cookie（优先级最高）
./edgetts -engine doubao -doubao-cookie "sessionid=..." -o output.mp3 "指定 Cookie"

# 强制重新从 Edge 提取 Cookie
./edgetts -engine doubao -doubao-refresh -o output.mp3 "刷新登录态"

# 列出豆包全部可用音色
./edgetts -engine doubao -l

# 只列某个分类
./edgetts -engine doubao -l -dv-tab female
```

豆包音色分类可选值：`rec`（推荐）、`female`（女声）、`male`（男声）、`characters`（角色）、`accent`（口音）。
列表输出的第一列即 `-dv` 可用的 `style_id`，例如 `zh_female_wenroutaozi_uranus_bigtts`。

豆包引擎的输出格式：

- `-o` 以 `.ogg` / `.opus` 结尾：直接写入原始 Ogg Opus 数据
- 其他情况（含默认路径）：调用 `ffmpeg` 转码为 MP3；`ffmpeg` 不可用时保留同名 `.ogg` 文件并提示

## 豆包 Cookie 说明

获取优先级：

1. `-doubao-cookie` 参数
2. 环境变量 `DOUBAO_COOKIE`
3. 本地缓存 `~/.config/edgetts-speechd/doubao.json`（权限 0600）
4. 自动从 Edge 提取（仅 Linux）

自动提取的做法：复制 Edge 的 `Local State` 与 `Default/Cookies` 到临时目录，启动 headless Edge 并通过 CDP 的 `Storage.getCookies` 读取 `doubao.com` 域的 Cookie。

Cookie 失效时（合成返回 `40200001` / `session auth`，音色列表返回 `710012001` / `登录已过期`），程序会：

1. 删除本地缓存；
2. 无论 Cookie 来自参数、环境变量还是缓存，都重新从 Edge 提取；
3. 用新 Cookie 重试一次。

仍失败时提示：

```
豆包登录态失效，请重新登录 Edge 中的豆包或设置 DOUBAO_COOKIE
```

自动提取依赖 Edge 中已登录豆包。若提取失败，可在浏览器开发者工具中复制 Cookie，然后：

```bash
export DOUBAO_COOKIE="sessionid=...; ttwid=...; ..."
```

注意：登录态失效后的重试一定会重新从 Edge 提取，即使设置了 `-doubao-cookie` 或 `DOUBAO_COOKIE`。因此只要 Edge 中保持登录，通常不需要手动维护 Cookie。

## 代理

Edge TTS 通过 `HTTPS_PROXY` / `HTTP_PROXY` 环境变量走代理；豆包 TTS 为直连，不使用代理。

旧版本的 `-p` 参数表示代理地址，现已改为音调，代理请改用环境变量。

## 全部参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-engine` | `edge` | TTS 引擎：`edge` 或 `doubao` |
| `-v` | 空 | Edge 说话人 |
| `-dv` | `zh_female_wenroutaozi_uranus_bigtts` | 豆包说话人 |
| `-r` | 空 | 语速：Edge 用 `+0%`；豆包用 `+10%` 或 `1.1` |
| `-vol` | 空 | 音量，仅 Edge |
| `-p` | 空 | 音调：Edge 用 `+0Hz`；豆包用 `-2` |
| `-o` | 空 | 只生成音频文件不播放 |
| `--no-split` | false | 禁用句子分割 |
| `--no-continuous` | false | 禁用连续播放 |
| `-l` | false | 列出当前引擎的可用语音 |
| `-dv-tab` | 空 | 列出豆包音色时限定分类：`rec`/`female`/`male`/`characters`/`accent` |
| `-doubao-cookie` | 空 | 直接指定豆包 Cookie |
| `-doubao-refresh` | false | 强制重新从 Edge 提取豆包 Cookie |
| `-h` / `--help` | false | 显示帮助 |

## 输出与退出码

- 音频路径、语音列表写到标准输出，便于管道处理（如 `awk '{print $1}'`）
- `[INFO]` / `[WARN]` 与错误信息写到标准错误
- 成功退出码为 `0`；参数错误、列表拉取失败、生成或播放失败时退出码为 `1`
- `-dv-tab` 与 `-doubao-refresh` 仅在豆包引擎下生效，用于 Edge 时会在 stderr 给出 `[WARN]`

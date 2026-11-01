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
```

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

Cookie 失效时（服务端返回 `40200001` / `session auth`），程序会删除缓存、重新提取并重试一次。仍失败时提示：

```
豆包登录态失效，请重新登录 Edge 中的豆包或设置 DOUBAO_COOKIE
```

自动提取依赖 Edge 中已登录豆包。若提取失败，可在浏览器开发者工具中复制 Cookie，然后：

```bash
export DOUBAO_COOKIE="sessionid=...; ttwid=...; ..."
```

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
| `-l` | false | 列出 Edge 可用语音 |
| `-doubao-cookie` | 空 | 直接指定豆包 Cookie |
| `-doubao-refresh` | false | 强制重新从 Edge 提取豆包 Cookie |
| `-h` / `--help` | false | 显示帮助 |

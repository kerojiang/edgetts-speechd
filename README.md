# edgetts-speechd

use the edgetts for the speech-dispatcher backend

实现原理和 edge-tts 相同，通过 edge tts 的地址通信获取音频数据，自带音频播放不依赖 mpv 等第三方播放器

配合 speech-dispatcher 的配置文件，实现了 linux 下默认 tts 调用 edgetts,foliate 软件能听书，orca 也能使用 edgetts 语音

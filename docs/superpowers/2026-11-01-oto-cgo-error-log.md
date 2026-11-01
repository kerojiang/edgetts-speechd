# 纠错记录：oto 是否需要 cgo

- 日期：2026-11-01
- 违反条款：`AGENTS.md` 执行要求第 1 条「所有结论必须有据可查，禁止猜测性结论」

## 错误结论

在回答"oto 能不能用"时，我断言：

> 几乎所有 Linux 上可用的 Go 音频播放库（gopxl/beep、ebitengine/oto、portaudio）底层都是 cgo（调用 ALSA/PulseAudio C 库），没有 C 编译器就无法编译。

并据此要求用户安装 gcc/base-devel。

## 实际事实（实测，有据可查）

1. `oto v3.5.1` 的 Linux 驱动 `driver_alsa_unix.go` build tag：
   ```go
   //go:build ((linux && !android) || freebsd || netbsd) && !nintendosdk && !playstation5
   ```
   **无 `cgo` 要求**，纯 Go 经 syscall 调 ALSA。
2. 本机（`CGO_ENABLED=0`、无任何 C 编译器）实测：
   ```
   build OK
   开始播放 0.5s 静音...
   Player err: <nil>
   关闭完成，成功
   ```
   编译、打开 ALSA 设备、播放、关闭全部成功。

## 根因

- 凭旧印象（oto v2 时代确实 cgo + ALSA 头文件）推广到当前 v3.5.1，**未核实当前版本源码**。
- 未先跑最小验证（`go build` + 运行）就下结论。

## 纠正

- 结论撤销，更正为：**oto v3.5.1 在 Linux 是纯 Go，无需 gcc、无需装系统包**。
- 用户原判断（"oto 这个不是可以吗"）正确。

## 防再犯规则（承诺）

1. 凡涉及库/API/技术可行性的结论，**必须先跑最小验证**（编译、运行、读当前版本源码）再写入回答；未验证的只能标「待验证」，不得断言。
2. 每违反一次，在本文件追加一条记录并当面向用户说明。

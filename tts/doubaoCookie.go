/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:doubaoCookie.go
 * 修改日期:2026/11/01
 * 作者:kerojiang
 */

package tts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 豆包登录 Cookie 的获取与本地持久化。
//
// 优先级：显式参数 > 环境变量 DOUBAO_COOKIE > 本地缓存 > 从 Edge 自动提取。
// 缓存不设固定过期时间，由服务端返回的 40200001 判定失效后重新提取。

const (
	// doubaoConfigDirName 是配置目录名，位于用户配置目录下。
	doubaoConfigDirName = "edgetts-speechd"
	// doubaoCookieFileName 是 Cookie 缓存文件名。
	doubaoCookieFileName = "doubao.json"
	// envDoubaoCookie 是用户直接指定 Cookie 的环境变量。
	envDoubaoCookie = "DOUBAO_COOKIE"
)

// doubaoConfigDirOverride 供测试覆盖配置目录。
var doubaoConfigDirOverride string

// doubaoEdgeExtractor 是“从 Edge 提取 Cookie”的注入点，由 doubaoEdgeCookie.go 赋值。
// 测试中可替换，避免启动真实浏览器。
var doubaoEdgeExtractor func() (string, error)

// doubaoCredentials 是缓存文件的 JSON 结构，只保存 Cookie 与更新时间。
type doubaoCredentials struct {
	Cookie    string    `json:"cookie"`
	UpdatedAt time.Time `json:"updated_at"`
}

// doubaoConfigDir 返回豆包配置目录。
func doubaoConfigDir() string {
	if doubaoConfigDirOverride != "" {
		return doubaoConfigDirOverride
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, doubaoConfigDirName)
}

// doubaoConfigPath 返回 Cookie 缓存文件路径。
func doubaoConfigPath() string {
	return filepath.Join(doubaoConfigDir(), doubaoCookieFileName)
}

// loadCachedDoubaoCookie 读取本地缓存的 Cookie。
func loadCachedDoubaoCookie() (string, error) {
	raw, err := os.ReadFile(doubaoConfigPath())
	if err != nil {
		return "", err
	}

	var creds doubaoCredentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return "", fmt.Errorf("解析豆包 Cookie 缓存失败: %w", err)
	}

	cookie := strings.TrimSpace(creds.Cookie)
	if cookie == "" {
		return "", fmt.Errorf("豆包 Cookie 缓存为空: %s", doubaoConfigPath())
	}
	return cookie, nil
}

// saveDoubaoCookie 把 Cookie 写入本地缓存，文件权限固定为 0600。
func saveDoubaoCookie(cookie string) error {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return errors.New("豆包 Cookie 为空，拒绝保存")
	}

	dir := doubaoConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	raw, err := json.MarshalIndent(doubaoCredentials{
		Cookie:    cookie,
		UpdatedAt: time.Now(),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 Cookie 失败: %w", err)
	}

	path := doubaoConfigPath()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开 Cookie 缓存失败: %w", err)
	}
	defer file.Close()

	// 文件已存在时 OpenFile 不会修改权限，这里显式收紧。
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("设置 Cookie 缓存权限失败: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		return fmt.Errorf("写入 Cookie 缓存失败: %w", err)
	}
	return nil
}

// clearDoubaoCookie 删除本地 Cookie 缓存；文件不存在不算错误。
func clearDoubaoCookie() error {
	err := os.Remove(doubaoConfigPath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 Cookie 缓存失败: %w", err)
	}
	return nil
}

// ClearDoubaoCookie 删除本地 Cookie 缓存，供命令行 -doubao-refresh 使用。
func ClearDoubaoCookie() error {
	return clearDoubaoCookie()
}

// resolveDoubaoCookie 按优先级解析可用的豆包 Cookie。
// 缓存缺失或损坏时自动从 Edge 提取，并写回缓存。
func resolveDoubaoCookie(explicit string) (string, error) {
	if cookie := strings.TrimSpace(explicit); cookie != "" {
		return cookie, nil
	}

	if cookie := strings.TrimSpace(os.Getenv(envDoubaoCookie)); cookie != "" {
		return cookie, nil
	}

	if cookie, err := loadCachedDoubaoCookie(); err == nil && cookie != "" {
		return cookie, nil
	}

	if doubaoEdgeExtractor == nil {
		return "", fmt.Errorf("未找到豆包 Cookie：请设置 %s，或登录 Edge 中的豆包", envDoubaoCookie)
	}

	cookie, err := doubaoEdgeExtractor()
	if err != nil {
		return "", fmt.Errorf("自动获取豆包 Cookie 失败: %w（请登录 Edge 中的豆包，或设置 %s）",
			err, envDoubaoCookie)
	}

	// 写缓存失败不影响本次调用，下次会自动重新提取。
	_ = saveDoubaoCookie(cookie)
	return cookie, nil
}

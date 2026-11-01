/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:doubaoEdgeCookie.go
 * 修改日期:2026/11/01
 * 作者:kerojiang
 */

package tts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// 从本机 Edge 的登录态中提取 doubao.com Cookie。
//
// Edge 的 Cookie 数据库由系统密钥环加密，直接解密不可靠，因此这里复制一份
// 用户配置到临时目录，再启动 headless Edge 通过 CDP 的 Storage.getCookies 读取。

// cdpCookie 是 CDP Storage.getCookies 返回的 Cookie 结构（只取需要的字段）。
type cdpCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

// edgeCookieExtractTimeout 是等待 Edge 调试端口就绪的最长时间。
const edgeCookieExtractTimeout = 20 * time.Second

// init 注册 Edge 提取实现，供 resolveDoubaoCookie 调用。
func init() {
	doubaoEdgeExtractor = extractDoubaoCookieFromEdge
}

// buildCookieHeader 把 CDP 返回的 Cookie 拼成 Cookie 请求头。
// 非 doubao.com 域名、空值与同名重复项会被丢弃（保留首次出现）。
func buildCookieHeader(items []cdpCookie) string {
	parts := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))

	for _, item := range items {
		if !strings.Contains(item.Domain, "doubao.com") {
			continue
		}
		if item.Value == "" || seen[item.Name] {
			continue
		}
		seen[item.Name] = true
		parts = append(parts, item.Name+"="+item.Value)
	}

	return strings.Join(parts, "; ")
}

// extractDoubaoCookieFromEdge 从 Edge 提取 doubao.com Cookie。
func extractDoubaoCookieFromEdge() (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("自动提取 Cookie 仅支持 Linux，请设置 %s", envDoubaoCookie)
	}

	edgeBinary, err := findEdgeBinary()
	if err != nil {
		return "", err
	}

	profileDir, err := edgeProfileDir()
	if err != nil {
		return "", err
	}

	tempDir, err := os.MkdirTemp("", "edgetts-edge-*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// 复制 Edge 配置，避免与正在运行的 Edge 抢锁。
	if err := copyFile(filepath.Join(profileDir, "Local State"),
		filepath.Join(tempDir, "Local State")); err != nil {
		return "", fmt.Errorf("复制 Edge Local State 失败: %w", err)
	}
	if err := copyFile(filepath.Join(profileDir, "Default", "Cookies"),
		filepath.Join(tempDir, "Default", "Cookies")); err != nil {
		return "", fmt.Errorf("复制 Edge Cookies 失败: %w", err)
	}

	port, err := freeTCPPort()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(edgeBinary,
		"--user-data-dir="+tempDir,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--headless=new",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		"--disable-extensions",
		"about:blank",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动 Edge 失败: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	wsURL, err := waitCDPTarget(port, edgeCookieExtractTimeout)
	if err != nil {
		return "", err
	}

	cookies, err := cdpGetCookies(wsURL)
	if err != nil {
		return "", err
	}

	header := buildCookieHeader(cookies)
	if header == "" {
		return "", errors.New("Edge 中未找到 doubao.com 登录 Cookie，请先在 Edge 中登录豆包")
	}
	return header, nil
}

// findEdgeBinary 在 PATH 中查找 Edge 可执行文件。
func findEdgeBinary() (string, error) {
	candidates := []string{
		"microsoft-edge",
		"microsoft-edge-stable",
		"microsoft-edge-beta",
		"msedge",
	}
	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("未找到 Microsoft Edge 可执行文件，请设置 " + envDoubaoCookie)
}

// edgeProfileDir 返回 Edge 的用户配置根目录。
func edgeProfileDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return "", fmt.Errorf("无法定位用户配置目录: %w", err)
	}
	dir := filepath.Join(base, "microsoft-edge")
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("未找到 Edge 配置目录 %s: %w", dir, err)
	}
	return dir, nil
}

// copyFile 复制文件并创建目标目录，目标权限为 0600。
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

// freeTCPPort 让系统分配一个空闲端口后立即释放。
func freeTCPPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("获取空闲端口失败: %w", err)
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("获取空闲端口失败: 地址类型异常")
	}
	return addr.Port, nil
}

// waitCDPTarget 轮询 CDP 的 /json/list，返回第一个 page 目标的 WebSocket 地址。
func waitCDPTarget(port int, timeout time.Duration) (string, error) {
	listURL := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := client.Get(listURL)
		if err == nil {
			var targets []struct {
				Type                 string `json:"type"`
				URL                  string `json:"url"`
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()

			if decodeErr == nil {
				for _, target := range targets {
					if target.Type == "page" && target.WebSocketDebuggerURL != "" {
						return target.WebSocketDebuggerURL, nil
					}
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}

	return "", fmt.Errorf("等待 Edge 调试端口超时（端口 %d，%s）", port, timeout)
}

// cdpGetCookies 通过 CDP 读取浏览器全部 Cookie。
func cdpGetCookies(wsURL string) ([]cdpCookie, error) {
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 Edge 调试协议失败: %w", err)
	}
	defer conn.Close()

	request := map[string]any{
		"id":     1,
		"method": "Storage.getCookies",
		"params": map[string]any{},
	}
	if err := conn.WriteJSON(request); err != nil {
		return nil, fmt.Errorf("发送 Storage.getCookies 失败: %w", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, fmt.Errorf("设置 CDP 读取超时失败: %w", err)
	}

	for {
		var message struct {
			ID     int `json:"id"`
			Result struct {
				Cookies []cdpCookie `json:"cookies"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := conn.ReadJSON(&message); err != nil {
			return nil, fmt.Errorf("读取 CDP 响应失败: %w", err)
		}
		if message.Error != nil {
			return nil, fmt.Errorf("CDP 返回错误: %s", message.Error.Message)
		}
		if message.ID == 1 {
			return message.Result.Cookies, nil
		}
	}
}

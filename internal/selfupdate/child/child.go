// Package child 提供面板服务进程（go-panel serve）与 supervisor 协作所需的辅助函数：
// 解析 supervisor 注入的环境变量，以及在监听成功后发送一次就绪通知。
package child

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
)

// Info 是 supervisor 注入给服务进程的运行信息；未被 supervisor 托管时 Supervised 为 false。
type Info struct {
	Supervised         bool
	Protocol           int
	RunMode            string
	BuiltinVersion     string
	BuiltinCommit      string
	BuiltinRuntimeABI  int
	BuiltinSchemaEpoch int
	RequestID          string
	RuntimeDir         string
}

// FromEnv 解析 supervisor 环境变量；无法解析的字段取零值。只有协议号为正整数时才视为被托管。
func FromEnv(getenv func(string) string) Info {
	if getenv == nil {
		getenv = os.Getenv
	}
	value := func(key string) string { return strings.TrimSpace(getenv(key)) }
	info := Info{
		RunMode:            value(protocol.EnvRunMode),
		BuiltinVersion:     value(protocol.EnvBuiltinVersion),
		BuiltinCommit:      value(protocol.EnvBuiltinCommit),
		BuiltinRuntimeABI:  positiveInt(value(protocol.EnvBuiltinRuntimeABI)),
		BuiltinSchemaEpoch: positiveInt(value(protocol.EnvBuiltinSchemaEpoch)),
		RequestID:          value(protocol.EnvRequestID),
		RuntimeDir:         value(protocol.EnvRuntimeDir),
	}
	if version := positiveInt(value(protocol.EnvSupervisorProtocol)); version > 0 {
		info.Supervised = true
		info.Protocol = version
	}
	return info
}

func positiveInt(raw string) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// Notifier 向 supervisor 发送一次就绪消息；未被托管时所有方法都是空操作。
type Notifier struct {
	mu   sync.Mutex
	file *os.File
	sent bool
}

// ClaimReadyPipe 必须在服务进程启动的最早期调用，早于打开数据库等任何文件。
//
// 只有环境变量指向的描述符确实是管道时才接管：若 supervisor 没有真正传入描述符，
// 同一编号稍后可能被数据库等普通文件占用，向其写入就绪消息会损坏数据。
// 接管后清除环境变量并设置 CLOEXEC，防止被之后启动的子进程继承。
func ClaimReadyPipe() *Notifier {
	raw := strings.TrimSpace(os.Getenv(protocol.EnvReadyFD))
	if raw == "" {
		return &Notifier{}
	}
	_ = os.Unsetenv(protocol.EnvReadyFD)
	fd, err := strconv.Atoi(raw)
	if err != nil {
		return &Notifier{}
	}
	return claimFD(fd)
}

func claimFD(fd int) *Notifier {
	// 0–2 是标准输入输出；非管道描述符不能包装成 os.File，否则 GC 终结器会关闭别人的描述符。
	if fd < protocol.ReadyFD || !isPipe(fd) {
		return &Notifier{}
	}
	file := os.NewFile(uintptr(fd), "go-panel-ready")
	if file == nil {
		return &Notifier{}
	}
	setCloseOnExec(fd)
	return &Notifier{file: file}
}

// Enabled 报告是否持有有效的就绪管道。
func (n *Notifier) Enabled() bool {
	if n == nil {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.file != nil && !n.sent
}

// Notify 写入 "READY=1 ADDR=<addr>\n" 后关闭管道，只生效一次。
func (n *Notifier) Notify(addr net.Addr) error {
	if n == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.file == nil || n.sent {
		return nil
	}
	n.sent = true
	message := protocol.ReadyMessagePrefix
	if addr != nil {
		message += " ADDR=" + addr.String()
	}
	_, writeErr := n.file.WriteString(message + "\n")
	closeErr := n.file.Close()
	n.file = nil
	return errors.Join(writeErr, closeErr)
}

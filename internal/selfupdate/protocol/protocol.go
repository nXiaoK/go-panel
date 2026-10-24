// Package protocol 定义 supervisor（go-panel supervise）与面板服务进程（go-panel serve）
// 之间的稳定契约：环境变量、就绪消息、退出码与协议版本。
//
// 这是跨版本契约：镜像中的旧 supervisor 会启动数据卷中更新的子进程。修改任何名称或取值
// 都必须提升协议号，并让新版本继续兼容旧协议（见 docs/soft-update/README.md §4.4）。
package protocol

const (
	// SupervisorProtocol 是本版本 supervisor 实现的协议号。
	SupervisorProtocol = 1
	// ChildProtocolMin 与 ChildProtocolMax 是本版本子进程能配合的 supervisor 协议范围。
	ChildProtocolMin = 1
	ChildProtocolMax = 1
)

// 环境变量名称。GO_PANEL_ 前缀只由 supervisor 注入；PANEL_ 与 NODE_ 前缀也可由运维配置。
const (
	EnvSupervisorProtocol = "GO_PANEL_SUPERVISOR_PROTOCOL"
	EnvReadyFD            = "GO_PANEL_READY_FD"
	EnvRunMode            = "GO_PANEL_RUN_MODE"
	EnvBuiltinVersion     = "GO_PANEL_BUILTIN_VERSION"
	EnvBuiltinCommit      = "GO_PANEL_BUILTIN_COMMIT"
	EnvBuiltinRuntimeABI  = "GO_PANEL_BUILTIN_RUNTIME_ABI"
	EnvBuiltinSchemaEpoch = "GO_PANEL_BUILTIN_SCHEMA_EPOCH"
	EnvRequestID          = "GO_PANEL_REQUEST_ID"
	EnvRuntimeDir         = "PANEL_RUNTIME_DIR"
	EnvUseBuiltin         = "PANEL_USE_BUILTIN"
	EnvNodeAssetDir       = "NODE_ASSET_DIR"
)

// 子进程运行方式，经 EnvRunMode 传递，仅用于状态展示。
const (
	RunModeBuiltin = "builtin"
	RunModeRelease = "release"
	RunModeTrial   = "trial"
)

const (
	// ExitRestartRequested（EX_TEMPFAIL）表示 pending.json 已写好，请 supervisor 重新选择版本。
	ExitRestartRequested = 75
	// ExitSchemaTooNew（EX_CONFIG）表示数据库要求的最低读取纪元高于本版本。
	ExitSchemaTooNew = 78
)

const (
	// ReadyFD 是 supervisor 通过 ExtraFiles[0] 传入的就绪管道。
	ReadyFD = 3
	// ReadyMessagePrefix 是就绪消息前缀，完整格式为 "READY=1 ADDR=<host:port>\n"。
	ReadyMessagePrefix = "READY=1"
)

// Range 表示闭区间 [Min, Max]。
type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Contains 判断 value 是否位于闭区间内。
func (r Range) Contains(value int) bool {
	return value >= r.Min && value <= r.Max
}

// Compat 是二进制声明的兼容信息，同时出现在 version --json 输出与更新清单中。
type Compat struct {
	RuntimeABI         int    `json:"runtimeAbi"`
	SchemaEpoch        int    `json:"schemaEpoch"`
	MinReaderEpoch     int    `json:"minReaderEpoch"`
	SupervisorProtocol Range  `json:"supervisorProtocol"`
	MinUpgradeFrom     string `json:"minUpgradeFrom"`
}

// VersionReport 是 go-panel version --json 的输出，CI 生成清单与更新预检都依赖其字段名。
type VersionReport struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	Compat
}

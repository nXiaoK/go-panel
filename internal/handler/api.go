package handler

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/service"
)

const (
	// nodeUpgradeResponseTimeout 只覆盖升级接口，需高于节点 WebSocket 命令的 90 秒等待上限。
	nodeUpgradeResponseTimeout = 2 * time.Minute
	// speedTestResponseGrace 为启动 iperf3（最多 5 秒）、客户端收尾（20 秒）及网络调度保留余量。
	// 仅在钳制后的测速时长上追加，避免用户输入无限延长连接占用。
	speedTestResponseGrace = 45 * time.Second
	// backupTransferTimeout 只覆盖最大 256 MB 的备份；512 KB/s 上传约需 8.5 分钟，
	// 额外时间留给数据库快照/校验。恢复仍受上传大小限制，慢连接最多占用 10 分钟。
	backupTransferTimeout = 10 * time.Minute
)

// bindJSON 绑定请求体，失败时返回友好提示（不回显底层解析细节，避免信息泄露）。
// 保留 HTTP 200 + code 字段的约定（与 Java 版 R 结构一致，前端按 code 判断）。
func bindJSON(c *gin.Context, obj interface{}) bool {
	if err := c.ShouldBindJSON(obj); err != nil {
		c.JSON(http.StatusOK, result.Err("请求参数格式错误，请检查必填字段与类型"))
		return false
	}
	return true
}

// ===== user =====

func login(c *gin.Context) {
	var req dto.LoginDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.Login(req, c.ClientIP()))
}

func createUser(c *gin.Context) {
	var req dto.UserDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CreateUser(req))
}

func listUsers(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetAllUsers())
}

func updateUser(c *gin.Context) {
	var req dto.UserUpdateDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateUser(req))
}

func deleteUser(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.DeleteUser(id))
}

func resetFlow(c *gin.Context) {
	var req dto.ResetFlowDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.ResetFlow(req))
}

func userPackage(c *gin.Context) {
	cu := currentUser(c)
	var req dto.UserPackageQueryDto
	_ = c.ShouldBindJSON(&req)
	c.JSON(http.StatusOK, service.GetUserPackageInfo(cu.UserID, req.Range, req.TunnelID))
}

func updatePassword(c *gin.Context) {
	var req dto.ChangePasswordDto
	if !bindJSON(c, &req) {
		return
	}
	cu := currentUser(c)
	c.JSON(http.StatusOK, service.UpdatePassword(cu.UserID, req))
}

// ===== node =====

func createNode(c *gin.Context) {
	var req dto.NodeDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CreateNode(req))
}

func listNodes(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetAllNodes())
}

func checkNodeStatus(c *gin.Context) {
	var req struct {
		NodeID int64 `json:"nodeId" binding:"required,min=1"`
	}
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CheckNodeStatus(req.NodeID))
}

func updateNode(c *gin.Context) {
	var req dto.NodeUpdateDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateNode(req))
}

func deleteNode(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.DeleteNode(id))
}

func nodeInstall(c *gin.Context) {
	var params map[string]interface{}
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusOK, result.Err("参数错误"))
		return
	}
	id, ok := toInt64(params["id"])
	if !ok {
		c.JSON(http.StatusOK, result.Err("id参数格式错误"))
		return
	}
	forwardMode := ""
	if v, ok := params["forwardMode"].(string); ok {
		forwardMode = v
	}
	c.JSON(http.StatusOK, service.GetInstallCommand(id, forwardMode))
}

func nodeUninstall(c *gin.Context) {
	var params map[string]interface{}
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusOK, result.Err("参数错误"))
		return
	}
	id, ok := toInt64(params["id"])
	if !ok {
		c.JSON(http.StatusOK, result.Err("id参数格式错误"))
		return
	}
	forwardMode := ""
	if v, ok := params["forwardMode"].(string); ok {
		forwardMode = v
	}
	c.JSON(http.StatusOK, service.GetUninstallCommand(id, forwardMode))
}

func nodeUpgrade(c *gin.Context) {
	// 节点升级命令会同步等待节点下载并确认，最长 90 秒；只为此接口延长写期限，避免放宽其他 API 的全局超时。
	if err := extendNodeUpgradeWriteDeadline(c.Writer, time.Now()); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Printf("延长节点升级响应写期限失败：%v", err)
	}
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.UpgradeNode(id))
}

func extendNodeUpgradeWriteDeadline(w http.ResponseWriter, now time.Time) error {
	return extendRequestDeadlines(w, now, 0, nodeUpgradeResponseTimeout)
}

// extendRequestDeadlines 只为已知长耗时接口延长读写期限，避免慢连接占满全局连接数。
// read/write 为 0 时保留原期限；底层不支持（如 ResponseRecorder）时返回 http.ErrNotSupported。
// 两种期限均会尝试设置，避免不支持其中一种时跳过另一种。
func extendRequestDeadlines(w http.ResponseWriter, now time.Time, read, write time.Duration) error {
	controller := http.NewResponseController(w)
	var readErr, writeErr error
	if read > 0 {
		readErr = controller.SetReadDeadline(now.Add(read))
	}
	if write > 0 {
		writeErr = controller.SetWriteDeadline(now.Add(write))
	}
	return errors.Join(readErr, writeErr)
}

// diagnosisDeadlineExtender 在探测开始前按最坏耗时（同节点串行 × 每条 10 秒）延长写期限，
// 上限由 service.DiagnosisResponseTimeout 钳制，避免固定 90 秒窗口截断多目标诊断。
func diagnosisDeadlineExtender(c *gin.Context, kind string) func(time.Duration) {
	return func(worstCase time.Duration) {
		if err := extendRequestDeadlines(c.Writer, time.Now(), 0, service.DiagnosisResponseTimeout(worstCase)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			log.Printf("延长%s诊断响应写期限失败：%v", kind, err)
		}
	}
}

// nftConfig nft 节点拉取规则（X-Node-Secret 头或 ?secret= 鉴权）
func nftConfig(c *gin.Context) {
	secret := nodeSecret(c)
	c.JSON(http.StatusOK, service.GetNftConfigBySecret(secret))
}

// ===== tunnel =====

func createTunnel(c *gin.Context) {
	var req dto.TunnelDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CreateTunnel(req))
}

func listTunnels(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetAllTunnels())
}

func getTunnel(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.GetTunnelByID(id))
}

func updateTunnel(c *gin.Context) {
	var req dto.TunnelUpdateDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateTunnel(req))
}

func deleteTunnel(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.DeleteTunnel(id))
}

func diagnoseTunnel(c *gin.Context) {
	id, ok := idFromBody(c, "tunnelId")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.DiagnoseTunnelWithBudget(id, diagnosisDeadlineExtender(c, "隧道")))
}

func speedTestTunnel(c *gin.Context) {
	var req dto.TunnelSpeedTestDto
	if !bindJSON(c, &req) {
		return
	}
	req.DurationSeconds = service.NormalizeSpeedTestDuration(req.DurationSeconds)
	if err := extendRequestDeadlines(c.Writer, time.Now(), 0, time.Duration(req.DurationSeconds)*time.Second+speedTestResponseGrace); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Printf("延长隧道测速响应写期限失败：%v", err)
	}
	c.JSON(http.StatusOK, service.SpeedTestTunnel(req))
}

func assignUserTunnel(c *gin.Context) {
	var req dto.UserTunnelDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.AssignUserTunnel(req))
}

func listUserTunnels(c *gin.Context) {
	var req dto.UserTunnelQueryDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.GetUserTunnelList(req.UserID))
}

func removeUserTunnel(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.RemoveUserTunnel(id))
}

func updateUserTunnel(c *gin.Context) {
	var req dto.UserTunnelUpdateDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateUserTunnel(req))
}

// userTunnelOptions 当前用户可用隧道
func userTunnelOptions(c *gin.Context) {
	cu := currentUser(c)
	c.JSON(http.StatusOK, service.UserTunnelList(cu.UserID, cu.RoleID))
}

func systemStatus(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetSystemStatus())
}

func systemVersion(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetBuildInfo())
}

func systemUpdateCheck(c *gin.Context) {
	// 检查包含 Release 查询与签名清单下载，给较慢的网络或下载代理留出 20 秒。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	c.JSON(http.StatusOK, service.CheckPanelUpdate(ctx))
}

// systemUpdateStatus 不访问网络，供界面轮询软更新作业进度与切换结果。
func systemUpdateStatus(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetPanelUpdateStatus())
}

func systemUpdateApply(c *gin.Context) {
	var req service.PanelUpdateApplyRequest
	if !bindOptionalStrictJSON(c, &req) {
		return
	}
	// 软更新只在此窗口内完成检查与作业创建，下载等耗时步骤在后台执行；侧车策略需完成检查与一致性备份。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	c.JSON(http.StatusOK, service.ApplyPanelUpdate(ctx, currentUser(c).UserName, req))
}

func systemUpdateCancel(c *gin.Context) {
	var req struct {
		JobID string `json:"jobId"`
	}
	if !bindOptionalStrictJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CancelPanelUpdate(req.JobID))
}

func systemUpdateRollback(c *gin.Context) {
	var req struct {
		Target string `json:"target"`
	}
	if !bindOptionalStrictJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.RequestPanelRollback(currentUser(c).UserName, req.Target))
}

// bindOptionalStrictJSON 严格解析可选的 JSON 对象请求体：空请求体视为 {}，未知字段与重复键返回参数错误。
func bindOptionalStrictJSON(c *gin.Context, dst any) bool {
	raw, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusOK, result.Err("参数错误"))
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	if err := decodeStrictJSONObject(raw, dst); err != nil {
		c.JSON(http.StatusOK, result.Err("参数错误"))
		return false
	}
	return true
}

// ===== forward =====

func createForward(c *gin.Context) {
	var req dto.ForwardDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CreateForward(currentUser(c), req))
}

func listForwards(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetAllForwards(currentUser(c)))
}

func updateForward(c *gin.Context) {
	var req dto.ForwardUpdateDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateForward(currentUser(c), req))
}

func deleteForward(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.DeleteForward(currentUser(c), id))
}

func forceDeleteForward(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.ForceDeleteForward(currentUser(c), id))
}

func pauseForward(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.PauseForward(currentUser(c), id))
}

func resumeForward(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.ResumeForward(currentUser(c), id))
}

func diagnoseForward(c *gin.Context) {
	id, ok := idFromBody(c, "forwardId")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.DiagnoseForwardWithBudget(currentUser(c), id, diagnosisDeadlineExtender(c, "转发")))
}

func updateForwardOrder(c *gin.Context) {
	var params struct {
		Forwards []map[string]interface{} `json:"forwards"`
	}
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusOK, result.Err("缺少forwards参数"))
		return
	}
	c.JSON(http.StatusOK, service.UpdateForwardOrder(currentUser(c), params.Forwards))
}

// detectNftRules 识别 NFT 中未在数据库的转发规则
func detectNftRules(c *gin.Context) {
	nodeID, ok := idFromBody(c, "nodeId")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.DetectNftForwardRules(nodeID))
}

// completeFromNft 从识别的 NFT 规则批量创建转发
func completeFromNft(c *gin.Context) {
	var req struct {
		NodeID int64                         `json:"nodeId"`
		Rules  []service.CompleteForwardRule `json:"rules"`
	}
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CompleteFromNft(currentUser(c), req.NodeID, req.Rules))
}

// detectTunnelRules 识别隧道转发规则（双节点）
func detectTunnelRules(c *gin.Context) {
	var req struct {
		InNodeID  int64 `json:"inNodeId"`
		OutNodeID int64 `json:"outNodeId"`
	}
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.DetectTunnelForwardRules(req.InNodeID, req.OutNodeID))
}

// ===== speed-limit =====

func createSpeedLimit(c *gin.Context) {
	var req dto.SpeedLimitDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CreateSpeedLimit(req))
}

func listSpeedLimits(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetAllSpeedLimits())
}

func updateSpeedLimit(c *gin.Context) {
	var req dto.SpeedLimitUpdateDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateSpeedLimit(req))
}

func deleteSpeedLimit(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.DeleteSpeedLimit(id))
}

// ===== config =====

func listConfigs(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetConfigsForUser(currentUser(c).UserID))
}

func getConfigByName(c *gin.Context) {
	var params map[string]interface{}
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusOK, result.Err("参数错误"))
		return
	}
	name, _ := params["name"].(string)
	c.JSON(http.StatusOK, service.GetPublicConfigByName(name))
}

func updateConfigs(c *gin.Context) {
	var configMap map[string]string
	if err := c.ShouldBindJSON(&configMap); err != nil {
		c.JSON(http.StatusOK, result.Err("配置数据不能为空"))
		return
	}
	c.JSON(http.StatusOK, service.UpdateConfigs(configMap))
}

func updateSingleConfig(c *gin.Context) {
	var params map[string]interface{}
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusOK, result.Err("参数错误"))
		return
	}
	name, _ := params["name"].(string)
	value, _ := params["value"].(string)
	c.JSON(http.StatusOK, service.UpdateConfig(name, value))
}

func deleteConfig(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.DeleteConfig(req.Name))
}

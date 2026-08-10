package gost

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

// 与 Java GostUtil 等价的命令构造层。
// 所有 JSON 结构（字段名/嵌套）与 Java fastjson 输出保持一致，节点端 go-gost 直接解析。

// J 任意 JSON 对象
type J = map[string]interface{}

// SuccessMsg gost 操作成功响应
const SuccessMsg = "OK"

// NotFoundMsg gost 资源不存在响应片段
const NotFoundMsg = "not found"

// sendLifecycleMessage is used by crash-recoverable mutations that must wait
// for either a definitive agent response or the selected session lifecycle.
// Unlike SendMsg it has no natural total timeout.
var sendLifecycleMessage = ws.SendMsgLifecycle

// IsOK 判断节点操作是否成功
func IsOK(res ws.GostResult) bool { return res.Msg == SuccessMsg }

// UnmarshalData 解析 WebSocket 响应的 data 字段
func UnmarshalData(data json.RawMessage, v interface{}) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

func limiterData(name int64, speed string) J {
	return J{
		"name":   strconv.FormatInt(name, 10),
		"limits": []string{"$ " + speed + "MB " + speed + "MB"},
	}
}

// AddLimiters 新增限速器
func AddLimiters(nodeID int64, name int64, speed string) ws.GostResult {
	return ws.SendMsg(nodeID, limiterData(name, speed), "AddLimiters")
}

// UpdateLimiters 更新限速器
func UpdateLimiters(nodeID int64, name int64, speed string) ws.GostResult {
	return ws.SendMsg(nodeID, J{
		"limiter": strconv.FormatInt(name, 10),
		"data":    limiterData(name, speed),
	}, "UpdateLimiters")
}

// DeleteLimiters 删除限速器
func DeleteLimiters(nodeID int64, name int64) ws.GostResult {
	return ws.SendMsg(nodeID, J{"limiter": strconv.FormatInt(name, 10)}, "DeleteLimiters")
}

// Limiter lifecycle variants have the same wire payloads as their bounded
// counterparts, but wait for a definitive response or session replacement.
func AddLimitersLifecycle(nodeID int64, name int64, speed string) ws.GostResult {
	return sendLifecycleMessage(nodeID, limiterData(name, speed), "AddLimiters")
}

func UpdateLimitersLifecycle(nodeID int64, name int64, speed string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{
		"limiter": strconv.FormatInt(name, 10),
		"data":    limiterData(name, speed),
	}, "UpdateLimiters")
}

func DeleteLimitersLifecycle(nodeID int64, name int64) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{"limiter": strconv.FormatInt(name, 10)}, "DeleteLimiters")
}

func forwarderNodes(remoteAddr string) []J {
	nodes := make([]J, 0, 2)
	i := 0
	for _, addr := range strings.Split(remoteAddr, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		i++
		nodes = append(nodes, J{
			"name": "node_" + strconv.Itoa(i),
			"addr": addr,
		})
	}
	return nodes
}

func forwarder(remoteAddr, strategy string) J {
	if strategy == "" {
		strategy = "fifo"
	}
	return J{
		"nodes": forwarderNodes(remoteAddr),
		"selector": J{
			"strategy":    strategy,
			"maxFails":    1,
			"failTimeout": "600s",
		},
	}
}

// serviceConfig 单协议服务配置（对应 GostUtil.createServiceConfig）
func serviceConfig(name string, inPort int, limiter *int64, remoteAddr, protocol string, fowType int, tunnel *model.Tunnel, strategy, interfaceName string) J {
	service := J{"name": name + "_" + protocol}
	if protocol == "tcp" {
		service["addr"] = tunnel.TCPListenAddr + ":" + strconv.Itoa(inPort)
	} else {
		service["addr"] = tunnel.UDPListenAddr + ":" + strconv.Itoa(inPort)
	}

	if interfaceName != "" {
		service["metadata"] = J{"interface": interfaceName}
	}

	if limiter != nil {
		service["limiter"] = strconv.FormatInt(*limiter, 10)
	}

	handler := J{"type": protocol}
	if fowType != 1 { // 隧道转发挂链
		handler["chain"] = name + "_chains"
	}
	service["handler"] = handler

	listener := J{"type": protocol}
	if protocol == "udp" {
		listener["metadata"] = J{"keepAlive": true}
	}
	service["listener"] = listener

	if fowType == 1 { // 端口转发需要转发器
		service["forwarder"] = forwarder(remoteAddr, strategy)
	}
	return service
}

func buildServices(name string, inPort int, limiter *int64, remoteAddr string, fowType int, tunnel *model.Tunnel, strategy, interfaceName string) []J {
	services := make([]J, 0, 2)
	for _, protocol := range []string{"tcp", "udp"} {
		services = append(services, serviceConfig(name, inPort, limiter, remoteAddr, protocol, fowType, tunnel, strategy, interfaceName))
	}
	return services
}

// AddService 新增主服务（tcp+udp 各一份）
func AddService(nodeID int64, name string, inPort int, limiter *int64, remoteAddr string, fowType int, tunnel *model.Tunnel, strategy, interfaceName string) ws.GostResult {
	return ws.SendMsg(nodeID, buildServices(name, inPort, limiter, remoteAddr, fowType, tunnel, strategy, interfaceName), "AddService")
}

func AddServiceLifecycle(nodeID int64, name string, inPort int, limiter *int64, remoteAddr string, fowType int, tunnel *model.Tunnel, strategy, interfaceName string) ws.GostResult {
	return sendLifecycleMessage(nodeID, buildServices(name, inPort, limiter, remoteAddr, fowType, tunnel, strategy, interfaceName), "AddService")
}

// UpdateService 更新主服务
func UpdateService(nodeID int64, name string, inPort int, limiter *int64, remoteAddr string, fowType int, tunnel *model.Tunnel, strategy, interfaceName string) ws.GostResult {
	return ws.SendMsg(nodeID, buildServices(name, inPort, limiter, remoteAddr, fowType, tunnel, strategy, interfaceName), "UpdateService")
}

func UpdateServiceLifecycle(nodeID int64, name string, inPort int, limiter *int64, remoteAddr string, fowType int, tunnel *model.Tunnel, strategy, interfaceName string) ws.GostResult {
	return sendLifecycleMessage(nodeID, buildServices(name, inPort, limiter, remoteAddr, fowType, tunnel, strategy, interfaceName), "UpdateService")
}

// DeleteService 删除主服务
func DeleteService(nodeID int64, name string) ws.GostResult {
	return ws.SendMsg(nodeID, J{"services": []string{name + "_tcp", name + "_udp"}}, "DeleteService")
}

func DeleteServiceLifecycle(nodeID int64, name string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{"services": []string{name + "_tcp", name + "_udp"}}, "DeleteService")
}

// remoteServiceConfig 出口节点 relay 服务配置
func remoteServiceConfig(name string, outPort int, remoteAddr, protocol, strategy, interfaceName string) J {
	data := J{
		"name": name + "_tls",
		"addr": ":" + strconv.Itoa(outPort),
	}
	if interfaceName != "" {
		data["metadata"] = J{"interface": interfaceName}
	}
	data["handler"] = J{"type": "relay"}
	data["listener"] = J{"type": protocol}
	data["forwarder"] = forwarder(remoteAddr, strategy)
	return data
}

// AddRemoteService 新增出口 relay 服务
func AddRemoteService(nodeID int64, name string, outPort int, remoteAddr, protocol, strategy, interfaceName string) ws.GostResult {
	return ws.SendMsg(nodeID, []J{remoteServiceConfig(name, outPort, remoteAddr, protocol, strategy, interfaceName)}, "AddService")
}

// AddRemoteServiceLifecycle preserves the AddRemoteService wire payload while
// binding completion to the selected WebSocket session instead of a 10-second
// response deadline.
func AddRemoteServiceLifecycle(nodeID int64, name string, outPort int, remoteAddr, protocol, strategy, interfaceName string) ws.GostResult {
	return sendLifecycleMessage(nodeID, []J{remoteServiceConfig(name, outPort, remoteAddr, protocol, strategy, interfaceName)}, "AddService")
}

// UpdateRemoteService 更新出口 relay 服务
func UpdateRemoteService(nodeID int64, name string, outPort int, remoteAddr, protocol, strategy, interfaceName string) ws.GostResult {
	return ws.SendMsg(nodeID, []J{remoteServiceConfig(name, outPort, remoteAddr, protocol, strategy, interfaceName)}, "UpdateService")
}

func UpdateRemoteServiceLifecycle(nodeID int64, name string, outPort int, remoteAddr, protocol, strategy, interfaceName string) ws.GostResult {
	return sendLifecycleMessage(nodeID, []J{remoteServiceConfig(name, outPort, remoteAddr, protocol, strategy, interfaceName)}, "UpdateService")
}

// DeleteRemoteService 删除出口 relay 服务
func DeleteRemoteService(nodeID int64, name string) ws.GostResult {
	return ws.SendMsg(nodeID, J{"services": []string{name + "_tls"}}, "DeleteService")
}

// DeleteRemoteServiceLifecycle is the lifecycle-bound counterpart used by
// checked compensation paths.
func DeleteRemoteServiceLifecycle(nodeID int64, name string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{"services": []string{name + "_tls"}}, "DeleteService")
}

// PauseService 暂停主服务
func PauseService(nodeID int64, name string) ws.GostResult {
	return ws.SendMsg(nodeID, J{"services": []string{name + "_tcp", name + "_udp"}}, "PauseService")
}

func PauseServiceLifecycle(nodeID int64, name string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{"services": []string{name + "_tcp", name + "_udp"}}, "PauseService")
}

// ResumeService 恢复主服务
func ResumeService(nodeID int64, name string) ws.GostResult {
	return ws.SendMsg(nodeID, J{"services": []string{name + "_tcp", name + "_udp"}}, "ResumeService")
}

func ResumeServiceLifecycle(nodeID int64, name string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{"services": []string{name + "_tcp", name + "_udp"}}, "ResumeService")
}

// PauseRemoteService 暂停出口服务
func PauseRemoteService(nodeID int64, name string) ws.GostResult {
	return ws.SendMsg(nodeID, J{"services": []string{name + "_tls"}}, "PauseService")
}

func PauseRemoteServiceLifecycle(nodeID int64, name string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{"services": []string{name + "_tls"}}, "PauseService")
}

// ResumeRemoteService 恢复出口服务
func ResumeRemoteService(nodeID int64, name string) ws.GostResult {
	return ws.SendMsg(nodeID, J{"services": []string{name + "_tls"}}, "ResumeService")
}

func ResumeRemoteServiceLifecycle(nodeID int64, name string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{"services": []string{name + "_tls"}}, "ResumeService")
}

func chainNodes(name, remoteAddr, protocol, interfaceName string) []J {
	dialer := J{"type": protocol}
	if protocol == "quic" {
		dialer["metadata"] = J{"keepAlive": true, "ttl": "10s"}
	}
	nodes := make([]J, 0, 2)
	i := 0
	for _, addr := range strings.Split(remoteAddr, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		i++
		node := J{
			"name":      "node-" + name + "-" + strconv.Itoa(i),
			"addr":      addr,
			"connector": J{"type": "relay"},
			"dialer":    dialer,
		}
		if interfaceName != "" {
			node["interface"] = interfaceName
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// chainConfig 转发链配置
func chainConfig(name, remoteAddr, protocol, interfaceName, strategy string) J {
	nodes := chainNodes(name, remoteAddr, protocol, interfaceName)
	if strategy == "" {
		strategy = "fifo"
	}
	hop := J{
		"name":  "hop-" + name,
		"nodes": nodes,
	}
	if len(nodes) > 1 {
		hop["selector"] = J{
			"strategy":    strategy,
			"maxFails":    1,
			"failTimeout": "600s",
		}
	}
	return J{
		"name": name + "_chains",
		"hops": []J{hop},
	}
}

// AddChains 新增转发链
func AddChains(nodeID int64, name, remoteAddr, protocol, interfaceName string) ws.GostResult {
	return AddChainsWithStrategy(nodeID, name, remoteAddr, protocol, interfaceName, "fifo")
}

// AddChainsWithStrategy 新增带节点选择策略的转发链
func AddChainsWithStrategy(nodeID int64, name, remoteAddr, protocol, interfaceName, strategy string) ws.GostResult {
	return ws.SendMsg(nodeID, chainConfig(name, remoteAddr, protocol, interfaceName, strategy), "AddChains")
}

func AddChainsWithStrategyLifecycle(nodeID int64, name, remoteAddr, protocol, interfaceName, strategy string) ws.GostResult {
	return sendLifecycleMessage(nodeID, chainConfig(name, remoteAddr, protocol, interfaceName, strategy), "AddChains")
}

// UpdateChains 更新转发链
func UpdateChains(nodeID int64, name, remoteAddr, protocol, interfaceName string) ws.GostResult {
	return UpdateChainsWithStrategy(nodeID, name, remoteAddr, protocol, interfaceName, "fifo")
}

// UpdateChainsWithStrategy 更新带节点选择策略的转发链
func UpdateChainsWithStrategy(nodeID int64, name, remoteAddr, protocol, interfaceName, strategy string) ws.GostResult {
	return ws.SendMsg(nodeID, J{
		"chain": name + "_chains",
		"data":  chainConfig(name, remoteAddr, protocol, interfaceName, strategy),
	}, "UpdateChains")
}

func UpdateChainsWithStrategyLifecycle(nodeID int64, name, remoteAddr, protocol, interfaceName, strategy string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{
		"chain": name + "_chains",
		"data":  chainConfig(name, remoteAddr, protocol, interfaceName, strategy),
	}, "UpdateChains")
}

// DeleteChains 删除转发链
func DeleteChains(nodeID int64, name string) ws.GostResult {
	return ws.SendMsg(nodeID, J{"chain": name + "_chains"}, "DeleteChains")
}

func DeleteChainsLifecycle(nodeID int64, name string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{"chain": name + "_chains"}, "DeleteChains")
}

// ReconcileManagedRuntimeLifecycle asks the agent to remove only strict
// panel-managed service/chain names absent from this complete desired set.
func ReconcileManagedRuntimeLifecycle(nodeID int64, services, chains []string) ws.GostResult {
	return sendLifecycleMessage(nodeID, J{"services": services, "chains": chains}, "ReconcileManagedRuntime")
}

func upgradeNodeCommandData(baseURL, mode, latestVersion string, allowInsecure bool) J {
	return J{
		"baseUrl":       strings.TrimRight(baseURL, "/"),
		"mode":          mode,
		"latestVersion": latestVersion,
		"allowInsecure": allowInsecure,
	}
}

// UpgradeNode triggers the node-side self-upgrade flow.
func UpgradeNode(nodeID int64, baseURL, mode, latestVersion string, allowInsecure bool) ws.GostResult {
	return ws.SendMsgWithTimeout(nodeID, upgradeNodeCommandData(baseURL, mode, latestVersion, allowInsecure), "UpgradeNode", 90*time.Second)
}

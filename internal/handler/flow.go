package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/service"
)

// 节点流量/配置上报（对应 Java FlowController）

const successResponse = "ok"
const maxFlowUploadBodySize int64 = 2 << 20

// nodeSecret 优先从 X-Node-Secret 头读取节点密钥，回退到 query 的 secret（向后兼容旧节点）。
// 头传输可避免密钥落入反代 access log / 浏览器历史。
func nodeSecret(c *gin.Context) string {
	if s := c.GetHeader("X-Node-Secret"); s != "" {
		return s
	}
	return c.Query("secret")
}

// uploadFlowData POST /flow/upload （X-Node-Secret 头或 ?secret=）
func uploadFlowData(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFlowUploadBodySize)
	secret := nodeSecret(c)
	node, err := service.AuthenticateNodeSecret(secret)
	if err != nil {
		log.Printf("流量上报失败：无效的 secret (来源IP: %s)", c.ClientIP())
		flowError(c, http.StatusUnauthorized, "invalid node credentials")
		return
	}

	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("流量上报失败：读取请求体失败: %v (secret: %s)", err, maskSecret(secret))
		flowError(c, uploadReadErrorStatus(err), "invalid flow report")
		return
	}
	plain := crypto.DecryptIfNeeded(raw, secret)

	var flow dto.FlowDto
	if err := json.Unmarshal(plain, &flow); err != nil {
		log.Printf("流量上报失败：JSON 解析失败: %v (secret: %s, data: %s)", err, maskSecret(secret), truncateData(plain))
		flowError(c, http.StatusBadRequest, "invalid flow report")
		return
	}
	if flow.N == "web_api" {
		c.String(http.StatusOK, successResponse)
		return
	}

	err = model.DB.Transaction(func(tx *gorm.DB) error {
		return service.ApplyGostFlow(tx, node, flow)
	})
	if err != nil {
		respondApplyFlowError(c, err)
		return
	}
	service.EnforceGostFlowLimits(flow)
	c.String(http.StatusOK, successResponse)
}

// uploadNftFlowBatch POST /flow/nft-upload （X-Node-Secret 头或 ?secret=）
func uploadNftFlowBatch(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFlowUploadBodySize)
	secret := nodeSecret(c)
	node, err := service.AuthenticateNodeSecret(secret)
	if err != nil {
		log.Printf("NFT流量上报失败：无效的 secret (来源IP: %s)", c.ClientIP())
		flowError(c, http.StatusUnauthorized, "invalid node credentials")
		return
	}

	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("NFT流量上报失败：读取请求体失败: %v (secret: %s)", err, maskSecret(secret))
		flowError(c, uploadReadErrorStatus(err), "invalid flow report")
		return
	}
	plain := crypto.DecryptIfNeeded(raw, secret)

	var batch dto.NftFlowBatchDto
	if err := json.Unmarshal(plain, &batch); err != nil || len(batch.Items) == 0 {
		if err != nil {
			log.Printf("NFT流量上报失败：JSON 解析失败: %v (secret: %s)", err, maskSecret(secret))
		} else {
			log.Printf("NFT流量上报失败：批次为空 (secret: %s)", maskSecret(secret))
		}
		flowError(c, http.StatusBadRequest, "invalid flow report")
		return
	}

	err = model.DB.Transaction(func(tx *gorm.DB) error {
		for _, item := range batch.Items {
			if err := service.ApplyNftFlowItem(tx, node, item); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		respondApplyFlowError(c, err)
		return
	}
	service.EnforceNftFlowLimits(batch.Items)
	c.String(http.StatusOK, successResponse)
}

// uploadNftFlowBatchV2 POST /flow/nft-upload-v2 implements durable idempotent accounting.
func uploadNftFlowBatchV2(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFlowUploadBodySize)
	secret := nodeSecret(c)
	node, err := service.AuthenticateNodeSecret(secret)
	if err != nil {
		flowError(c, http.StatusUnauthorized, "invalid node credentials")
		return
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		flowError(c, uploadReadErrorStatus(err), "invalid flow report")
		return
	}
	plain := crypto.DecryptIfNeeded(raw, secret)
	var batch dto.NftFlowBatchV2Dto
	if err := decodeStrictJSONObject(plain, &batch); err != nil {
		flowError(c, http.StatusBadRequest, "invalid flow report")
		return
	}
	ack, err := service.ProcessNftBatch(node, batch)
	if err != nil {
		respondApplyFlowError(c, err)
		return
	}
	c.JSON(http.StatusOK, ack)
}

// uploadGostConfig POST /flow/config （X-Node-Secret 头或 ?secret=，gost 配置自检）
func uploadGostConfig(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFlowUploadBodySize)
	secret := nodeSecret(c)
	node, err := service.AuthenticateNodeSecret(secret)
	if err != nil {
		log.Printf("配置上报失败：无效的 secret (来源IP: %s)", c.ClientIP())
		flowError(c, http.StatusUnauthorized, "invalid node credentials")
		return
	}
	nodeID := strconv.FormatInt(node.ID, 10)

	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("配置上报失败：读取请求体失败: %v (nodeID: %s)", err, nodeID)
		flowError(c, uploadReadErrorStatus(err), "invalid config report")
		return
	}
	plain := crypto.DecryptIfNeeded(raw, secret)

	// gost 上报体外层为 {config: {...}}，内层才是 services/chains/limiters
	var wrapper struct {
		Config *dto.GostConfigDto `json:"config"`
	}
	var cfg dto.GostConfigDto
	if err := json.Unmarshal(plain, &wrapper); err == nil && wrapper.Config != nil {
		cfg = *wrapper.Config
	} else if err := json.Unmarshal(plain, &cfg); err != nil {
		log.Printf("配置上报失败：JSON 解析失败: %v (nodeID: %s)", err, nodeID)
		flowError(c, http.StatusBadRequest, "invalid config report")
		return
	}

	// 用 SafeGo 包装，避免 CleanNodeConfigs 内 panic 拖垮整个进程
	service.SafeGo("clean-node-configs", func() { service.CleanNodeConfigs(nodeID, cfg) })
	c.String(http.StatusOK, successResponse)
}

func uploadReadErrorStatus(err error) int {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func respondApplyFlowError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidFlowReport):
		flowError(c, http.StatusBadRequest, "invalid flow report")
	case errors.Is(err, service.ErrFlowNodeMismatch):
		flowError(c, http.StatusForbidden, "flow report rejected")
	case errors.Is(err, service.ErrFlowSequence), errors.Is(err, service.ErrFlowBatchConflict):
		flowError(c, http.StatusConflict, "flow report sequence rejected")
	default:
		log.Printf("流量上报事务失败: %v", err)
		flowError(c, http.StatusInternalServerError, "flow report failed")
	}
}

func flowError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}

// maskSecret 脱敏 secret，只显示前4位和后4位
func maskSecret(secret string) string {
	if len(secret) <= 8 {
		return "***"
	}
	return secret[:4] + "****" + secret[len(secret)-4:]
}

// truncateData 截断数据用于日志
func truncateData(data []byte) string {
	if len(data) > 100 {
		return string(data[:100]) + "..."
	}
	return string(data)
}

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/service"
)

func getR2BackupSettings(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetR2BackupSettings())
}

func updateR2BackupSettings(c *gin.Context) {
	var request service.R2BackupSettingsUpdate
	if !bindJSON(c, &request) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateR2BackupSettings(request))
}

func testR2BackupConnection(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	c.JSON(http.StatusOK, service.TestR2BackupConnection(ctx))
}

func runR2BackupNow(c *gin.Context) {
	// 手动上传必须在 HTTP 写超时前返回；自动任务使用独立的 2 分钟窗口。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	c.JSON(http.StatusOK, service.RunR2BackupNow(ctx))
}

func downloadBackup(c *gin.Context) {
	backup, err := service.CreateSiteBackup()
	if err != nil {
		c.JSON(http.StatusOK, result.Err(err.Error()))
		return
	}
	defer backup.Cleanup()

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, backup.Filename))
	c.Header("Content-Type", "application/vnd.sqlite3")
	c.File(backup.Path)
}

func restoreBackup(c *gin.Context) {
	if c.Request.ContentLength > service.MaxSiteBackupUploadSize+(1<<20) {
		c.JSON(http.StatusOK, result.Err(fmt.Sprintf("备份文件不能超过 %d MB", service.MaxSiteBackupUploadSize>>20)))
		return
	}
	// Multipart 表单除了文件内容还有 boundary/header 开销，额外保留 1MB 给表单封装。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxSiteBackupUploadSize+(1<<20))

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusOK, result.Err("请选择备份文件"))
		return
	}
	if fileHeader.Size <= 0 {
		c.JSON(http.StatusOK, result.Err("备份文件为空"))
		return
	}
	if fileHeader.Size > service.MaxSiteBackupUploadSize {
		c.JSON(http.StatusOK, result.Err(fmt.Sprintf("备份文件不能超过 %d MB", service.MaxSiteBackupUploadSize>>20)))
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusOK, result.Err("读取备份文件失败"))
		return
	}
	defer src.Close()

	tmpDir, err := os.MkdirTemp("", "flux-panel-restore-*")
	if err != nil {
		c.JSON(http.StatusOK, result.Err("创建恢复临时目录失败"))
		return
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, "upload.db")
	dst, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		c.JSON(http.StatusOK, result.Err("保存备份文件失败"))
		return
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		c.JSON(http.StatusOK, result.Err("保存备份文件失败"))
		return
	}
	if err := dst.Close(); err != nil {
		c.JSON(http.StatusOK, result.Err("保存备份文件失败"))
		return
	}

	summary, err := service.RestoreSiteBackup(tmpPath)
	if err != nil {
		c.JSON(http.StatusOK, result.Err(err.Error()))
		return
	}
	c.JSON(http.StatusOK, result.Ok(summary))
}

// detectExtraRules 检测所有节点的多余规则
func detectExtraRules(c *gin.Context) {
	c.JSON(http.StatusOK, service.DetectExtraRulesAfterRestore())
}

// handleExtraRules 处理多余规则
func handleExtraRules(c *gin.Context) {
	var req service.HandleExtraRulesRequest
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.HandleExtraRules(currentUser(c), &req))
}

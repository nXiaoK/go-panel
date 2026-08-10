package handler

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed assets/apply_nft_rules.sh assets/install.sh assets/install_nftables.sh assets/uninstall_gost.sh assets/uninstall_nftables.sh
var nodeInstallAssets embed.FS

var allowedNodeScripts = map[string]bool{
	"apply_nft_rules.sh":    true,
	"install.sh":            true,
	"install_nftables.sh":   true,
	"uninstall_gost.sh":     true,
	"uninstall_nftables.sh": true,
}

var allowedNodeBinaries = map[string]bool{
	"gost-amd64":              true,
	"gost-arm64":              true,
	"nft_agent_amd64":         true,
	"nft_agent_arm64":         true,
	"nft_rule_payload_amd64":  true,
	"nft_rule_payload_arm64":  true,
	"nft_flow_reporter_amd64": true,
	"nft_flow_reporter_arm64": true,
}

func nodeInstallScript(c *gin.Context) {
	name := c.Param("name")
	if !allowedNodeScripts[name] {
		c.String(http.StatusNotFound, "node script not found")
		return
	}

	data, err := fs.ReadFile(nodeInstallAssets, "assets/"+name)
	if err != nil {
		c.String(http.StatusNotFound, "node script not found")
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "text/x-shellscript; charset=utf-8", data)
}

func nodeBinaryAsset(c *gin.Context) {
	name := c.Param("name")
	if !allowedNodeBinaries[name] || strings.Contains(name, "/") || strings.Contains(name, "..") {
		c.String(http.StatusNotFound, "node asset not found")
		return
	}

	path := filepath.Join(nodeAssetDir(), name)
	if _, err := os.Stat(path); err != nil {
		c.String(http.StatusNotFound, "node asset %s not found on panel server", name)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.File(path)
}

func nodeAssetDir() string {
	if dir := strings.TrimSpace(os.Getenv("NODE_ASSET_DIR")); dir != "" {
		return dir
	}
	return "node-assets"
}

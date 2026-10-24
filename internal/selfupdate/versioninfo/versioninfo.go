// Package versioninfo 汇总本二进制的构建信息与兼容声明，供 version 子命令、发布工具和更新预检使用。
package versioninfo

import (
	"runtime"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
)

// CurrentCompat 返回编译进本二进制的兼容声明。
func CurrentCompat() protocol.Compat {
	return protocol.Compat{
		RuntimeABI:         buildinfo.RuntimeABI,
		SchemaEpoch:        model.SchemaEpoch,
		MinReaderEpoch:     model.MinReaderEpoch,
		SupervisorProtocol: protocol.Range{Min: protocol.ChildProtocolMin, Max: protocol.ChildProtocolMax},
		MinUpgradeFrom:     buildinfo.MinUpgradeFrom,
	}
}

// Current 返回 go-panel version --json 的完整内容；不读取配置、不访问数据库和网络。
func Current() protocol.VersionReport {
	build := buildinfo.Current()
	return protocol.VersionReport{
		Version:   build.Version,
		Commit:    build.Commit,
		BuildTime: build.BuildTime,
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		Compat:    CurrentCompat(),
	}
}

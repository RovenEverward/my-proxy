// Package bwtest 提供带宽测量用的内嵌 sing-box 客户端(UC-20)。
// 把 render.Endpoint 渲染的最小 sing-box 配置在进程内拉起,本机经它
// 定量下载得到节点真实吞吐;不依赖外部核心二进制。
package bwtest

import (
	"context"
	"fmt"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
)

// startBox 解析一份 sing-box 客户端配置并在进程内启动,返回可关闭的实例。
func startBox(config []byte) (*box.Box, error) {
	ctx := include.Context(context.Background())
	var opt option.Options
	if err := opt.UnmarshalJSONContext(ctx, config); err != nil {
		return nil, fmt.Errorf("解析 sing-box 配置: %w", err)
	}
	b, err := box.New(box.Options{Context: ctx, Options: opt})
	if err != nil {
		return nil, fmt.Errorf("构造 sing-box: %w", err)
	}
	if err := b.Start(); err != nil {
		b.Close()
		return nil, fmt.Errorf("启动 sing-box: %w", err)
	}
	return b, nil
}

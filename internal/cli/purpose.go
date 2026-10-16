package cli

import (
	"encoding/json"
	"fmt"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
	"net/url"
	"os"
	"strconv"
)

func init() { rootCmd.AddCommand(newPurposeCmd()) }
func newPurposeCmd() *cobra.Command {
	c := &cobra.Command{Use: "purpose", Short: "(已废弃,v0.7.0 删除)管理用途分组、标签与订阅预览",
		// cobra 只跑最近的一个 PersistentPreRun:先打警告,再接着跑根命令的(读配置档等)。
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.ErrOrStderr(), "警告:mpx purpose 已废弃,将在 v0.7.0 删除。分流改用 mpx rule / mpx group;节点地区改用 mpx node edit --region。")
			if rootCmd.PersistentPreRunE != nil {
				return rootCmd.PersistentPreRunE(cmd, args)
			}
			return nil
		},
		Long: `已废弃:v0.6.0 起不再维护,v0.7.0 删除。请改用 mpx rule / mpx group。

管理日常、流媒体、下载和游戏用途。用途通过 regions / lines / tags 筛选已授权节点；
 required_capabilities 可选 egress / youtube / netflix，capability_region 为两位大写地区代码。
 下载与游戏需配置 domains；url-test 测量的是客户端 HTTP 响应延迟，不代表下载吞吐、游戏 RTT 或丢包。`}
	c.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "列出用途", RunE: func(cmd *cobra.Command, args []string) error {
		cl, e := newClient()
		if e != nil {
			return e
		}
		raw, e := cl.do("GET", "/api/purposes", nil)
		if e != nil {
			return e
		}
		return emitJSON(raw)
	}})
	c.AddCommand(&cobra.Command{Use: "get SLUG", Args: cobra.ExactArgs(1), Short: "读取完整用途 JSON", RunE: func(cmd *cobra.Command, args []string) error {
		cl, e := newClient()
		if e != nil {
			return e
		}
		raw, e := cl.do("GET", "/api/purposes/"+url.PathEscape(args[0]), nil)
		if e != nil {
			return e
		}
		return emitJSON(raw)
	}})
	for _, verb := range []string{"create", "set"} {
		verb := verb
		var file string
		cmd := &cobra.Command{Use: verb + " SLUG -f FILE", Short: "用完整 JSON 创建或更新用途（- 为 stdin）", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return fmt.Errorf("必须给 -f FILE")
			}
			b, e := readSettingsFile(file)
			if e != nil {
				return e
			}
			var p model.Purpose
			if e = json.Unmarshal(b, &p); e != nil {
				return e
			}
			if p.Slug != "" && p.Slug != args[0] {
				return fmt.Errorf("slug 与参数不一致")
			}
			p.Slug = args[0]
			p.Normalize()
			if e = p.Validate(); e != nil {
				return e
			}
			cl, e := newClient()
			if e != nil {
				return e
			}
			method, path := "POST", "/api/purposes"
			if verb == "set" {
				method, path = "PUT", path+"/"+url.PathEscape(p.Slug)
			}
			raw, e := cl.do(method, path, p)
			if e != nil {
				return e
			}
			return report(raw, "用途 %s 已保存", p.Slug)
		}}
		cmd.Flags().StringVarP(&file, "file", "f", "", "用途 JSON 文件")
		c.AddCommand(cmd)
	}
	c.AddCommand(&cobra.Command{Use: "del SLUG", Short: "删除用途，已发出的用途 URL 随即失效", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, e := newClient()
		if e != nil {
			return e
		}
		raw, e := cl.do("DELETE", "/api/purposes/"+url.PathEscape(args[0]), nil)
		if e != nil {
			return e
		}
		return report(raw, "用途 %s 已删除", args[0])
	}})
	c.AddCommand(&cobra.Command{Use: "preview SLUG USER FORMAT", Short: "预览用户实际候选和路由配置（含凭证）", Args: cobra.ExactArgs(3), RunE: func(cmd *cobra.Command, args []string) error {
		if args[2] != "clash-meta" && args[2] != "sing-box" {
			return fmt.Errorf("用途预览只支持 clash-meta / sing-box")
		}
		cl, e := newClient()
		if e != nil {
			return e
		}
		raw, e := cl.do("GET", "/api/purposes/"+url.PathEscape(args[0])+"/preview/"+url.PathEscape(args[1])+"/"+args[2], nil)
		if e != nil {
			return e
		}
		_, e = os.Stdout.Write(raw)
		return e
	}})
	metadata := &cobra.Command{Use: "metadata", Short: "读取或保存节点/host 的地区、线路、用途标签"}
	for _, verb := range []string{"get", "set"} {
		verb := verb
		var file string
		cmd := &cobra.Command{Use: verb + " nodes|hosts ID", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "nodes" && args[0] != "hosts" {
				return fmt.Errorf("类型只能是 nodes / hosts")
			}
			id, e := strconv.ParseInt(args[1], 10, 64)
			if e != nil || id <= 0 {
				return fmt.Errorf("ID 必须为正整数")
			}
			var body any
			method := "GET"
			if verb == "set" {
				if file == "" {
					return fmt.Errorf("必须给 -f FILE")
				}
				b, e := readSettingsFile(file)
				if e != nil {
					return e
				}
				var m model.PurposeMetadata
				if e = json.Unmarshal(b, &m); e != nil {
					return e
				}
				if e = m.Validate(); e != nil {
					return e
				}
				body = m
				method = "PUT"
			}
			cl, e := newClient()
			if e != nil {
				return e
			}
			raw, e := cl.do(method, "/api/"+args[0]+"/"+args[1]+"/metadata", body)
			if e != nil {
				return e
			}
			return emitJSON(raw)
		}}
		if verb == "set" {
			cmd.Flags().StringVarP(&file, "file", "f", "", "元数据 JSON 文件")
		}
		metadata.AddCommand(cmd)
	}
	c.AddCommand(metadata)
	return c
}

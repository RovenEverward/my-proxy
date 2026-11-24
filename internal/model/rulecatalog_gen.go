// 内置规则组目录:统一使用 MetaCubeX/meta-rules-dat(meta 分支 .mrs + sing 分支 .srs),
// 与 sing-box / Mihomo 同源对齐。TestCatalogMatchesDefaults 会检查默认模板与此处一致。

package model

const (
	metaGeositeBase = "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@meta/geo/geosite/"
	metaGeoIPBase   = "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@meta/geo/geoip/"
	singGeositeBase = "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/geosite/"
	singGeoIPBase   = "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/geoip/"
)

var ruleCatalog = []RuleSet{
	{
		Key: "lan", Name: "局域网与本机", Desc: "路由器、NAS、localhost 等内网地址", Default: "DIRECT", PrivateIP: true,
		Clash: []ClashRuleSet{
			{Name: "private", Behavior: "domain", URL: metaGeositeBase + "private.mrs"},
			{Name: "private-ip", Behavior: "ipcidr", URL: metaGeoIPBase + "private.mrs", NoResolve: true},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "private", URL: singGeositeBase + "private.srs"},
		},
	},
	{
		Key: "ads", Name: "广告拦截", Desc: "常见广告与跟踪域名（按需启用）", Default: "REJECT",
		Clash: []ClashRuleSet{
			{Name: "ads", Behavior: "domain", URL: metaGeositeBase + "category-ads-all.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "ads", URL: singGeositeBase + "category-ads-all.srs"},
		},
	},
	{
		Key: "ai", Name: "AI 服务", Desc: "ChatGPT、Claude、Gemini、Cursor、Copilot、Perplexity 等", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "ai", Behavior: "domain", URL: metaGeositeBase + "category-ai-!cn.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "ai", URL: singGeositeBase + "category-ai-!cn.srs"},
		},
	},
	{
		Key: "media", Name: "流媒体", Desc: "YouTube、Netflix、Disney+、HBO、Spotify、TikTok 等", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "youtube", Behavior: "domain", URL: metaGeositeBase + "youtube.mrs"},
			{Name: "netflix", Behavior: "domain", URL: metaGeositeBase + "netflix.mrs"},
			{Name: "disney", Behavior: "domain", URL: metaGeositeBase + "disney.mrs"},
			{Name: "hbo", Behavior: "domain", URL: metaGeositeBase + "hbo.mrs"},
			{Name: "primevideo", Behavior: "domain", URL: metaGeositeBase + "primevideo.mrs"},
			{Name: "spotify", Behavior: "domain", URL: metaGeositeBase + "spotify.mrs"},
			{Name: "twitch", Behavior: "domain", URL: metaGeositeBase + "twitch.mrs"},
			{Name: "tiktok", Behavior: "domain", URL: metaGeositeBase + "tiktok.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "youtube", URL: singGeositeBase + "youtube.srs"},
			{Tag: "netflix", URL: singGeositeBase + "netflix.srs"},
			{Tag: "disney", URL: singGeositeBase + "disney.srs"},
			{Tag: "hbo", URL: singGeositeBase + "hbo.srs"},
			{Tag: "primevideo", URL: singGeositeBase + "primevideo.srs"},
			{Tag: "spotify", URL: singGeositeBase + "spotify.srs"},
			{Tag: "twitch", URL: singGeositeBase + "twitch.srs"},
			{Tag: "tiktok", URL: singGeositeBase + "tiktok.srs"},
		},
	},
	{
		Key: "telegram", Name: "Telegram", Desc: "Telegram 域名与 IP 段", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "telegram", Behavior: "domain", URL: metaGeositeBase + "telegram.mrs"},
			{Name: "telegram-ip", Behavior: "ipcidr", URL: metaGeoIPBase + "telegram.mrs", NoResolve: true},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "telegram", URL: singGeositeBase + "telegram.srs"},
			{Tag: "telegram-ip", URL: singGeoIPBase + "telegram.srs"},
		},
	},
	{
		Key: "microsoft", Name: "微软", Desc: "Microsoft、OneDrive、Outlook、Bing", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "microsoft", Behavior: "domain", URL: metaGeositeBase + "microsoft.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "microsoft", URL: singGeositeBase + "microsoft.srs"},
		},
	},
	{
		Key: "apple", Name: "苹果", Desc: "App Store、iCloud、Apple Music", Default: "DIRECT",
		Clash: []ClashRuleSet{
			{Name: "apple", Behavior: "domain", URL: metaGeositeBase + "apple.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "apple", URL: singGeositeBase + "apple.srs"},
		},
	},
	{
		Key: "google", Name: "谷歌", Desc: "搜索、Drive、FCM 推送", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "google", Behavior: "domain", URL: metaGeositeBase + "google.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "google", URL: singGeositeBase + "google.srs"},
		},
	},
	{
		Key: "social", Name: "社交", Desc: "X、Discord、Reddit、Facebook、Instagram 等", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "social", Behavior: "domain", URL: metaGeositeBase + "category-social-media-!cn.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "social", URL: singGeositeBase + "category-social-media-!cn.srs"},
		},
	},
	{
		Key: "game", Name: "海外游戏", Desc: "Steam、PlayStation、Xbox、Nintendo 等海外平台", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "games", Behavior: "domain", URL: metaGeositeBase + "category-games-!cn.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "games", URL: singGeositeBase + "category-games-!cn.srs"},
		},
	},
	{
		Key: "dev", Name: "开发", Desc: "GitHub、Docker、npm、PyPI、Cloudflare 等", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "dev", Behavior: "domain", URL: metaGeositeBase + "category-dev.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "dev", URL: singGeositeBase + "category-dev.srs"},
		},
	},
	{
		Key: "github", Name: "GitHub", Desc: "GitHub 网站与代码下载", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "github", Behavior: "domain", URL: metaGeositeBase + "github.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "github", URL: singGeositeBase + "github.srs"},
		},
	},
	{
		Key: "cnmedia", Name: "国内媒体", Desc: "B 站、爱奇艺、腾讯视频、网易云音乐等", Default: "DIRECT",
		Clash: []ClashRuleSet{
			{Name: "cnmedia", Behavior: "domain", URL: metaGeositeBase + "category-entertainment@cn.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "cnmedia", URL: singGeositeBase + "category-entertainment@cn.srs"},
		},
	},
	{
		Key: "global", Name: "海外常用", Desc: "常见受限海外域名（GFW 列表）", Default: "PROXY",
		Clash: []ClashRuleSet{
			{Name: "gfw", Behavior: "domain", URL: metaGeositeBase + "gfw.mrs"},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "gfw", URL: singGeositeBase + "gfw.srs"},
		},
	},
	{
		Key: "cn", Name: "国内网站", Desc: "国内域名、国内游戏 CDN 与国内 IP 段", Default: "DIRECT",
		Clash: []ClashRuleSet{
			{Name: "cn", Behavior: "domain", URL: metaGeositeBase + "cn.mrs"},
			{Name: "cn-games", Behavior: "domain", URL: metaGeositeBase + "category-games@cn.mrs"},
			{Name: "cn-ip", Behavior: "ipcidr", URL: metaGeoIPBase + "cn.mrs", NoResolve: true},
		},
		SingBox: []SingBoxRuleSet{
			{Tag: "cn", URL: singGeositeBase + "cn.srs"},
			{Tag: "cn-games", URL: singGeositeBase + "category-games@cn.srs"},
			{Tag: "cn-ip", URL: singGeoIPBase + "cn.srs"},
		},
	},
}

// Package codearts 码道（华为云 CodeArts Agent）消费通道。
//
// 移植来源：HITZY2002/codearts2api（MIT License, © 2026 HITZY2002）的
// internal/upstream/{const.go,signer.go}。我们只取其协议层（签名/端点/常量），
// 不带它的账号池、WebUI、签到逻辑——那些分别由我们的 agg 层与 L0NE 签到脚本负责。
package codearts

// 常量：CodeArts Agent 云端 API（源自官方 VS Code 扩展 vscode-codebot 的逆向结果）。
const (
	// SnapEngineApiHost 盘古引擎/聊天网关（snap-access）。
	SnapEngineApiHost = "https://snap-access.cn-north-4.myhuaweicloud.com"
	// SnapManagerHost 登录/令牌服务。
	SnapManagerHost = SnapEngineApiHost + "/snap-manager"
	// PortalHost 华为云 CodeArts 门户（OAuth authorize 入口，注意带 /portal）。
	PortalHost = "https://codearts.huaweicloud.com/portal"
	// IAMHost IAM 令牌/STS（备用）。
	IAMHost = "https://iam.myhuaweicloud.com"
	// STSHost 临时 AK/SK。
	STSHost = "https://sts.cn-north-4.myhuaweicloud.com"

	// ClientID OAuth client（官方插件的 uri_scheme）。refresh_token 与该值绑定：
	// 换 client_id 刷新会被 STS 拒（invalid client id）。实测 2026-09-15。
	ClientID = "codearts-agent"

	// 聊天端点。
	EpChatV2         = "/api/v2/chat/completions"
	EpChat           = "/v1/chat/chat"
	EpStartChat      = "/v1/chat/start-chat"
	EpChatAgents     = "/v1/chat/agents"
	EpChatManagement = "/v1/chat/management/event"
	EpChatRecord     = "/v1/chat/record-request"
	EpAgentList      = "/v1/agent-center/agents/useragents"
	EpAgentDetail    = "/v1/agent-center/agents/detail"
	// EpModelBuiltin 内置模型（与官方 ModelService.listBuiltinModels 一致，Agent-Type: PromptCenter）。
	EpModelBuiltin = "/v1/model/builtin"

	// 限时福利（免费套餐）网关：模型发现 + 领取。
	BenefitHost     = "https://opengw.developer.huaweicloud.com"
	EpBenefitConfig = "/api/v1/gateway/config"
	EpBenefitClaim  = "/api/v1/benefit/claim"

	// 登录/令牌。
	EpLoginTicket    = "/v1/login/ticket"
	EpOAuthTokens    = "/v1/oauth2/tokens"
	EpCurrentUser    = "/v1/current/user"
	EpCallerIdentity = "/v5/caller-identity"
)

// 客户端身份头。码道是"签名制"：服务端验 AK/SK 签名即认身份，不吃 TLS 指纹，
// 所以这两个头抄准即可（见计划书 §三之二）。
const (
	HdrAgentType = "Agent-Type"
	AgentType    = "PromptCenter"
	HdrLanguage  = "X-Language"
	LangZHCN     = "zh-cn"
)

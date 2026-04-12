package llm

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"nozomi/internal/config"

	"google.golang.org/genai"
)

// GenerateResult 结构化大模型的返回结果
type GenerateResult struct {
	RawText    string        // 带有完整排版的原始文本（用于发给 Matrix）
	CleanParts []*genai.Part // 剔除了思考过程的纯净上下文（用于存入 Memory）
	UsedSearch bool          // 是否使用了联网搜索
	CostTime   time.Duration
}

// TokenUsage 封装 Token 消耗，用于安全地交给 Billing 领域算账
type TokenUsage struct {
	Input  int32
	Output int32
	Think  int32
}

type Client struct {
	apiClient *genai.Client
	cfg       *config.ModelConfig
}

func NewClient(botCfg *config.BotConfig) (*Client, error) {
	ctx := context.Background() // 仅用于初始化阶段
	apiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  botCfg.Model.API_KEY,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}

	// 装配大模型参数
	botCfg.Model.Config = &genai.GenerateContentConfig{
		MaxOutputTokens:   botCfg.Model.MaxOutputToken,
		SystemInstruction: genai.Text(botCfg.Model.Soul)[0],
	}

	// 思考配置
	if botCfg.Model.IncludeThoughts {
		botCfg.Model.Config.ThinkingConfig = &genai.ThinkingConfig{
			IncludeThoughts: botCfg.Model.IncludeThoughts,
		}
		if botCfg.Model.ThinkingBudget > 0 {
			botCfg.Model.Config.ThinkingConfig.ThinkingBudget = &botCfg.Model.ThinkingBudget
		} else if botCfg.Model.ThinkingLevel != "" {
			botCfg.Model.Config.ThinkingConfig.ThinkingLevel = genai.ThinkingLevel(botCfg.Model.ThinkingLevel)
		} else {
			botCfg.Model.Config.ThinkingConfig.ThinkingLevel = genai.ThinkingLevelUnspecified
		}
	}

	// 工具与安全审查配置
	if botCfg.Model.UseInternet {
		botCfg.Model.Config.Tools = []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}
	}
	if !botCfg.Model.SecureCheck {
		botCfg.Model.Config.SafetySettings = []*genai.SafetySetting{
			{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockNone},
		}
	}

	return &Client{
		apiClient: apiClient,
		cfg:       &botCfg.Model,
	}, nil
}

// GetConfigWithoutSearch 返回一个去除了 Tools 工具的深拷贝配置
func (c *Client) GetConfigWithoutSearch() *genai.GenerateContentConfig {
	if c.cfg.Config == nil {
		return nil
	}
	temp := *c.cfg.Config // 深拷贝
	temp.Tools = nil
	return &temp
}

// Generate 封装了大模型的调用、提取、过滤逻辑
// 参数 dynamicConfig 允许外层在搜索配额耗尽时，传入一个剥夺了 Tools 的浅拷贝配置
func (c *Client) Generate(ctx context.Context, history []*genai.Content, dynamicConfig *genai.GenerateContentConfig) (*GenerateResult, *TokenUsage, error) {
	// 1. 设置严格的超时熔断
	timeoutCtx, cancel := context.WithTimeout(ctx, c.cfg.TimeOutWhen)
	defer cancel()

	// 2. 发起 API 请求
	reqConf := c.cfg.Config
	if dynamicConfig != nil {
		reqConf = dynamicConfig
	}
	now := time.Now()
	resp, err := c.apiClient.Models.GenerateContent(timeoutCtx, c.cfg.Model, history, reqConf)
	costTime := time.Since(now)
	if err != nil {
		return nil, nil, err
	}

	// 3. 拦截静默报错与空回复
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil, nil, errors.New("the model returned a null value and has been inhibited")
	}

	// 4. 分离并计算 Token
	usage := &TokenUsage{
		Input:  resp.UsageMetadata.PromptTokenCount,
		Output: resp.UsageMetadata.CandidatesTokenCount,
	}
	usage.Think = resp.UsageMetadata.TotalTokenCount - usage.Output - usage.Input

	// 5. 提取文本与格式清洗
	raw := resp.Text()
	raw = strings.TrimSpace(raw)
	re := regexp.MustCompile(`\n{3,}`)
	raw = re.ReplaceAllString(raw, "\n\n")

	// 6. 构造纯净记忆块
	cleanParts := genai.Text(raw)[0].Parts

	// 7. 检查是否使用了联网搜寻
	usedSearch := false
	if len(resp.Candidates) > 0 && resp.Candidates[0].GroundingMetadata != nil {
		meta := resp.Candidates[0].GroundingMetadata
		if meta.SearchEntryPoint != nil || len(meta.GroundingChunks) > 0 {
			usedSearch = true
		}
	}

	result := &GenerateResult{
		RawText:    raw,
		CleanParts: cleanParts,
		UsedSearch: usedSearch,
		CostTime:   costTime,
	}

	return result, usage, nil
}

package app

import (
	"context"
	"log"

	"nozomi/internal/billing"
	"nozomi/internal/config"
	"nozomi/internal/handler"
	"nozomi/internal/llm"
	"nozomi/internal/logger"
	"nozomi/internal/matrix"
	"nozomi/internal/memory"
	"nozomi/internal/quota"
	"nozomi/internal/ratelimit"

	"maunium.net/go/mautrix/event"
)

type App struct {
	Config      *config.BotConfig
	Logger      *logger.Logger
	Matrix      *matrix.Client
	LLM         *llm.Client
	Memory      *memory.Manager
	Billing     *billing.System
	Router      *handler.Router
	Quota       *quota.Manager
	RateManager *ratelimit.RateManager
}

func NewApp(cfg *config.BotConfig) (*App, error) {
	// 初始化纯内存领域
	memMgr := memory.NewManager(cfg.Client.MaxMemoryLength, cfg.Client.WhenRetroRemainMemLen)

	// 初始化账单
	billSys := billing.NewSystem(cfg.Model.AlargmTokenCount, cfg.WorkDir)

	// 初始化 Gemini 大模型领域
	llmClient, err := llm.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	// 初始化 Matrix 协议交互领域
	matrixClient, err := matrix.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	// 初始化 Quota 联网额度
	quota := quota.NewManager(cfg.Model.MaxMonthlySearch, cfg.WorkDir)

	// 初始化 logger 日志记录器
	logger := logger.NewLogger(cfg)

	// 初始化 rateManager API限流
	rateManager := ratelimit.NewRateManager(cfg.Model.Rate, cfg.Model.RateBurst)

	app := &App{
		Config:      cfg,
		Logger:      logger,
		Matrix:      matrixClient,
		LLM:         llmClient,
		Memory:      memMgr,
		Billing:     billSys,
		Quota:       quota,
		RateManager: rateManager,
	}

	// 注册核心业务路由器
	app.Router = handler.NewRouter(app.Matrix, app.LLM, app.Memory, app.Billing, cfg, logger, quota, rateManager)

	return app, nil
}

// Start 负责启动网络层和所有后台 GC 任务
func (a *App) Start(ctx context.Context) error {
	// 启动后台任务线程
	a.StartBackgroundTasks(ctx)

	a.Matrix.OnEvent(event.EventMessage, a.Router.HandleMessage)
	a.Matrix.OnEvent(event.StateMember, a.Router.HandleMember)

	log.Println("Bot internal engines fully operational.")
	a.Logger.Log("info", "Bot internal engines fully operational.", logger.Options{})

	// 启动长连接同步
	return a.Matrix.Sync(ctx)
}

// Stop 释放所有底层资源
func (a *App) Stop() {
	if a.Matrix != nil {
		a.Matrix.Close()
	}
}

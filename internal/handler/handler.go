package handler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"nozomi/internal/billing"
	"nozomi/internal/config"
	"nozomi/internal/llm"
	"nozomi/internal/logger"
	"nozomi/internal/matrix"
	"nozomi/internal/memory"
	"nozomi/internal/quota"
	"nozomi/internal/ratelimit"

	"google.golang.org/genai"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type Router struct {
	matrix      *matrix.Client
	llm         *llm.Client
	memory      *memory.Manager
	billing     *billing.System
	cfg         *config.BotConfig
	logger      *logger.Logger
	quota       *quota.Manager
	rateManager *ratelimit.RateManager
	bootTime    time.Time // 用于过滤启动前的历史陈旧消息
}

func NewRouter(m *matrix.Client, l *llm.Client, mem *memory.Manager, b *billing.System, cfg *config.BotConfig, logger *logger.Logger, quota *quota.Manager, rateManager *ratelimit.RateManager) *Router {
	return &Router{
		matrix:      m,
		llm:         l,
		memory:      mem,
		billing:     b,
		cfg:         cfg,
		logger:      logger,
		quota:       quota,
		rateManager: rateManager,
		bootTime:    time.Now(),
	}
}

// HandleMessage 专门处理 m.room.message 事件
func (r *Router) HandleMessage(ctx context.Context, evt *event.Event) {
	// 1. 无视启动前的消息和自己发的消息
	if time.UnixMilli(evt.Timestamp).Before(r.bootTime) || evt.Sender == r.cfg.Client.UserID {
		return
	}

	// 2. 房间类型判断
	memberCount, err := r.matrix.GetRoomMemberCount(ctx, evt.RoomID.String())
	if err != nil {
		r.logger.Log("error", "Failed to get room member count: "+err.Error(), logger.Options{})
		return
	}
	isGroup := memberCount > 2

	// 2. 委托 Matrix 领域解析消息
	msgCtxs, err := r.matrix.ParseMessage(ctx, evt, 1)
	if err != nil {
		if err.Error() != "not a message event" && err.Error() != "Not support of gif image." {
			str := "用户：" + evt.Sender.String() + "\n"
			str += "房间：" + evt.RoomID.String() + "\n"
			str += "时间：" + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"
			str += "错误：" + "Failed to parse message: " + err.Error()

			_ = r.matrix.SendText(ctx, evt.RoomID, "Sorry, I need rest.Pls try again later.")
			_ = r.logger.Log("error", "Failed to parse message: "+err.Error(), logger.Options{})
			errs := r.matrix.SendToLogRoom(ctx, str)
			for _, err := range errs {
				str := "Sending log to log-room error: " + err.Error()
				_ = r.logger.Log("error", str, logger.Options{})
			}
		}
		return
	}
	msgCtxsLen := len(msgCtxs)
	if msgCtxsLen == 0 {
		return
	}
	finalText, finalImages := r.FormatMessageContexts(msgCtxs, isGroup)
	currentCtx := msgCtxs[len(msgCtxs)-1]

	roomID := evt.RoomID.String()
	sender := evt.Sender.String()

	// 4. 私聊逻辑
	if !isGroup {
		currentCtx.IsMentioned = true

		// 私聊特殊的连续发图合并逻辑
		isPureImageOrSticker := false
		if currentCtx.ImagePart != nil {
			matched, _ := regexp.MatchString(`^\(发送了一张(图片|贴纸)[^)]*\)\s*$`, currentCtx.Text)
			isPureImageOrSticker = matched
		}

		if isPureImageOrSticker {
			// 委托 Memory 领域暂存当前这张图
			imgCount := r.memory.AddPrivateImageCache(roomID, currentCtx.ImagePart)
			str := fmt.Sprintf("收到 %d 张图。请在 5 分钟内补充文字说明。", imgCount)
			_ = r.matrix.SendText(ctx, id.RoomID(roomID), str)
			return
		}
	}

	// 5. 委托 Memory 领域：记录群友说的话，并取出安全的上下文深拷贝
	history := r.memory.AddUserMsgAndLoad(roomID, finalText, finalImages...)

	// 6. 如果没有关键字，只记入记忆
	if !currentCtx.IsMentioned {
		return
	}

	if currentCtx.IsUnsupportedImageType {
		_ = r.matrix.SendText(ctx, evt.RoomID, "Not support this type of image.")
		return
	}

	// 高频拦截
	if !r.rateManager.AllowRequest(sender) {
		str := "拦截到高频请求：\n"
		str += "房间：" + roomID + "\n"
		str += "用户：" + sender
		r.matrix.SendText(ctx, evt.RoomID, "Sorry, I need rest.Please try again later.")
		errs := r.matrix.SendToLogRoom(ctx, str)
		for _, err := range errs {
			r.logger.Log("error", "Failed to send log to log-room: "+err.Error(), logger.Options{})
		}
		r.logger.Log("info", "Intercepted abnormally high frequency requests.", logger.Options{})
		return
	}

	// 7. 开启独立工作协程，不阻塞 Matrix 的主接收线程
	go func(safeHistory []*genai.Content, text string, sender id.UserID, rID id.RoomID) {
		bgCtx := context.Background()

		// 判断联网次数是否耗光
		var dynamicConfig *genai.GenerateContentConfig
		if r.quota.CheckAndGetRemaining() <= 0 {
			dynamicConfig = r.llm.GetConfigWithoutSearch()
		}

		// 委托 LLM 领域：发起思考与生成
		res, usage, err := r.llm.Generate(bgCtx, safeHistory, dynamicConfig)
		if err != nil {
			str := "用户：" + sender.String() + "\n"
			str += "房间：" + rID.String() + "\n"
			str += "请求：" + text + "\n"
			str += "时间：" + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"

			errMsg := err.Error()

			isLocalTimeout := errors.Is(err, context.DeadlineExceeded)
			isRemoteTimeout := strings.Contains(errMsg, "DEADLINE_EXCEEDED") || strings.Contains(errMsg, "504")
			if isLocalTimeout || isRemoteTimeout {
				str += "大模型调用超时！"
				_ = r.matrix.SendText(bgCtx, rID, "Network congestion.Please try again later.")
				_ = r.logger.Log("error", "Call LLM time out.", logger.Options{})
			} else {
				_ = r.matrix.SendText(bgCtx, rID, "Sorry, I need rest.Please try again later")
				_ = r.logger.Log("error", fmt.Sprintf("Gemini meet an error: %s", err.Error()), logger.Options{})

				str += "错误：" + err.Error()
			}

			errs := r.matrix.SendToLogRoom(bgCtx, str)
			for _, err := range errs {
				str := "Sending log to log-room error: " + err.Error()
				_ = r.logger.Log("error", str, logger.Options{})
			}
			return
		}

		if res.UsedSearch {
			// 委托 Quota 领域：扣减一次额度
			r.quota.Consume()
		}

		// 记录日志
		tokenConsume := fmt.Sprintf(
			" | 输入%d 输出%d 总计消耗%d | %v",
			usage.Input,
			usage.Output,
			usage.Think+usage.Input+usage.Output,
			res.CostTime,
		)
		_ = r.logger.Log("bot", text+tokenConsume, logger.Options{UserID: sender.String(), RoomID: rID.String()})

		// 委托 Billing 领域：安全地记账
		r.billing.Record(usage.Input, usage.Output, usage.Think)
		if r.billing.CheckAlarm(usage.Input + usage.Output + usage.Think) {
			str := "用量警报！\n"
			str += "用户：" + sender.String() + "\n"
			str += "房间：" + sender.String() + "\n"
			str += "请求：" + text + "\n"
			str += "时间: " + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"
			str += "Token 账单单次达到警报值！\n"
			str += tokenConsume
			errs := r.matrix.SendToLogRoom(bgCtx, str)
			for _, err := range errs {
				r.logger.Log("error", "Sending log to log-room error: "+err.Error(), logger.Options{})
			}
		}

		// 9. 委托 Memory 领域：将大模型的纯净回复写入记忆
		r.memory.AddModelMsg(rID.String(), res.CleanParts)

		// 10. 确认是否需要执行记忆回传算法
		nowHistoryLen := r.memory.GetHistoryLen(safeHistory)
		needMemoryRetrospection := nowHistoryLen >= r.cfg.Client.MaxMemoryLength && !isGroup
		if needMemoryRetrospection && r.memory.TryLockRoomSummarization(rID) {
			oldH, summarizedPartCount := r.memory.GetOldHistory(safeHistory)
			go func(oldH []*genai.Content, sumCount int, roomID id.RoomID) {
				defer r.memory.UnlockRoomSummarization(roomID)
				bgCtx := context.Background()
				str := "简要总结这段聊天记录的内容，不超过300字。"
				dynamicConfig := r.cfg.Model.Config
				if r.quota.CheckAndGetRemaining() <= 0 {
					dynamicConfig = r.llm.GetConfigWithoutSearch()
				}
				dynamicConfig.SystemInstruction = genai.Text(str)[0]
				var res *llm.GenerateResult
				var usage *llm.TokenUsage
				var err error
				for retry := 0; retry < 3; retry++ {
					res, usage, err = r.llm.Generate(bgCtx, oldH, dynamicConfig)
					if err != nil {
						time.Sleep(time.Second * 2)
						continue
					}
					break
				}
				if res.UsedSearch {
					r.quota.Consume()
				}
				r.billing.Record(usage.Input, usage.Output, usage.Think)
				err = r.memory.MemoryRetrospection(roomID, res.CleanParts, sumCount)
				if err != nil {
					str := fmt.Sprintf("Memort Retrospection for room %s failed: %v", evt.RoomID, err)
					r.logger.Log("error", str, logger.Options{})
				}
			}(oldH, summarizedPartCount, rID)
		}

		// 11. 委托 Matrix 领域：将富文本渲染并发送到房间
		err = r.matrix.SendMarkdown(bgCtx, rID, res.RawText)
		if err != nil {
			str := "用户：" + sender.String() + "\n"
			str += "房间：" + rID.String() + "\n"
			str += "请求：" + text + "\n"
			str += "时间：" + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"
			str += "错误：" + "Failed to send rich message to room: " + err.Error()
			_ = r.matrix.SendText(bgCtx, rID, "sorry, I need rest, please try again later.")
			_ = r.logger.Log("error", "Failed to send rich message to room: "+err.Error(), logger.Options{})
			errs := r.matrix.SendToLogRoom(bgCtx, str)
			for _, err := range errs {
				str := "Sending log to log-room error: " + err.Error()
				_ = r.logger.Log("error", str, logger.Options{})
			}
			return
		}
	}(history, currentCtx.Text, evt.Sender, evt.RoomID)
}

// HandleMember 专门处理 m.room.member 事件（原 evtMember.go 的终极进化版）
func (r *Router) HandleMember(ctx context.Context, evt *event.Event) {
	memberEvent := evt.Content.AsMember()
	if memberEvent == nil || evt.StateKey == nil || *evt.StateKey != string(r.cfg.Client.UserID) {
		return
	}

	switch memberEvent.Membership {
	case event.MembershipInvite:
		// 委托 Matrix 领域：自动同意加入房间
		rooms, err := r.matrix.GetJoinedRooms(ctx)
		if err != nil {
			_ = r.logger.Log("error", "Get joined rooms error: "+err.Error(), logger.Options{})
			return
		}
		for _, room := range rooms {
			if room == evt.RoomID.String() {
				return
			}
		}
		err = r.matrix.JoinRoom(ctx, evt.RoomID)
		if err == nil {
			_ = r.matrix.SendText(ctx, evt.RoomID, "你好，我是希。")
			_ = r.logger.Log("info", "Auto accept room invite: "+evt.RoomID.String(), logger.Options{})
		}
	case event.MembershipLeave, event.MembershipBan:
		// 委托 Memory 领域：被踢出或退出时，物理清除该房间的所有记忆
		r.memory.Delete(evt.RoomID.String())
		_ = r.logger.Log("info", "Auto clear memory of didn't join room: "+evt.RoomID.String(), logger.Options{})
	}
}

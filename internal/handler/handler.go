package handler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"nozomi/internal/billing"
	"nozomi/internal/config"
	"nozomi/internal/llm"
	"nozomi/internal/logger"
	"nozomi/internal/matrix"
	"nozomi/internal/memory"
	"nozomi/internal/quota"
	"nozomi/internal/ratelimit"
	"nozomi/tools"

	"google.golang.org/genai"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type Router struct {
	matrix           *matrix.Client
	llm              *llm.Client
	memory           *memory.Manager
	billing          *billing.System
	cfg              *config.BotConfig
	logger           *logger.Logger
	quota            *quota.Manager
	rateManager      *ratelimit.RateManager
	bootTime         time.Time // 用于过滤启动前的历史陈旧消息
	pendingApprovals sync.Map  // map[tools.Task]chan bool
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

// HandleMessage 专门处理群聊信息事件，包括 m.room.message 和 event.EventSticker
func (r *Router) HandleMessage(ctx context.Context, evt *event.Event) {
	// 无视启动前的消息和自己发的消息
	if time.UnixMilli(evt.Timestamp).Before(r.bootTime) || evt.Sender == r.cfg.Client.UserID {
		return
	}

	// 房间类型判断
	memberCount, err := r.matrix.GetRoomMemberCount(ctx, evt.RoomID.String())
	if err != nil {
		r.logger.Log("error", "Failed to get room member count: "+err.Error(), logger.Options{})
		return
	}
	isGroup := memberCount > 2

	// 解析消息
	msgCtxs, err := r.matrix.ParseMessage(ctx, evt, 1)
	if err != nil {
		if err.Error() != "not a message event" && err.Error() != "Not support of gif image." {
			str := "user: " + evt.Sender.String() + "\n"
			str += "room: " + evt.RoomID.String() + "\n"
			str += "time: " + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"
			str += "error: " + "Failed to parse message: " + err.Error()

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

	currentCtx := msgCtxs[len(msgCtxs)-1]

	// 指令检测
	if r.checkIsPendingTask(ctx, currentCtx.Text, evt.RoomID, evt.Sender) {
		return
	}

	finalText, finalImages := r.FormatMessageContexts(msgCtxs, isGroup)

	roomID := evt.RoomID.String()
	sender := evt.Sender.String()

	// 私聊逻辑
	if !isGroup {
		currentCtx.IsMentioned = true

		// 私聊特殊的连续发图合并逻辑
		isPureImageOrSticker := false
		if currentCtx.ImagePart != nil {
			matched, _ := regexp.MatchString(`^\(Send a (picture|sticker)[^)]*\)\s*$`, currentCtx.Text)
			isPureImageOrSticker = matched
		}

		if isPureImageOrSticker {
			// 暂存当前这张图
			imgCount := r.memory.AddPrivateImageCache(roomID, currentCtx.ImagePart)
			str := fmt.Sprintf("Receive picture %d.Please provide a written description within 5 minutes.", imgCount)
			_ = r.matrix.SendText(ctx, id.RoomID(roomID), str)
			return
		}
	}

	// 记录群友说的话，并取出安全的上下文深拷贝
	history := r.memory.AddUserMsgAndLoad(roomID, isGroup, finalText, finalImages...)

	// 如果没有关键字，只记入记忆
	if !currentCtx.IsMentioned {
		return
	}

	if currentCtx.IsUnsupportedImageType {
		_ = r.matrix.SendText(ctx, evt.RoomID, "Not support this type of image.")
		return
	}

	// 高频拦截
	if !r.rateManager.AllowRequest(sender) {
		str := "Intercepting high-frequency requests：\n"
		str += "room: " + roomID + "\n"
		str += "user: " + sender
		r.matrix.SendText(ctx, evt.RoomID, "Sorry, I need rest.Please try again later.")
		errs := r.matrix.SendToLogRoom(ctx, str)
		for _, err := range errs {
			r.logger.Log("error", "Failed to send log to log-room: "+err.Error(), logger.Options{})
		}
		r.logger.Log("info", "Intercepted abnormally high frequency requests.", logger.Options{})
		return
	}

	// 开启独立工作协程，不阻塞 Matrix 的主接收线程
	go func(safeHistory []*genai.Content, text string, sender id.UserID, rID id.RoomID, isGroup bool) {
		bgCtx := context.Background()

		// 发送已读回执
		err := r.matrix.MarkRead(bgCtx, rID, evt.ID)
		if err != nil {
			str := fmt.Sprintf("Failed to send read receipt to room %v: %v", rID, err)
			r.logger.Log("error", str, logger.Options{})
			r.matrix.SendToLogRoom(bgCtx, str)
		}

		// 模拟人类输入
		done := make(chan struct{})
		go func() {
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()

			select {
			case <-done:
				return
			case <-timer.C:
				_ = r.matrix.UserTyping(bgCtx, rID, true, r.cfg.Model.TimeOutWhen)
				<-done
				_ = r.matrix.UserTyping(bgCtx, rID, false, 0)
			}
		}()
		defer close(done)

		// 判断联网次数是否耗光
		var dynamicConfig *genai.GenerateContentConfig
		if r.quota.CheckAndGetRemaining() <= 0 {
			dynamicConfig = r.llm.GetConfigWithoutSearch()
		}

		// Call LLM
		var (
			res           *llm.GenerateResult
			totalUsage    *llm.TokenUsage = new(llm.TokenUsage)
			maxStep       int             = 10
			totalCostTime time.Duration
		)
		for retry := 0; retry < maxStep; retry++ {
			var usage *llm.TokenUsage
			res, usage, err = r.llm.Generate(bgCtx, safeHistory, dynamicConfig)
			if err != nil {
				r.logger.Log("error", err.Error(), logger.Options{})
				time.Sleep(1 * time.Second)
				continue
			}
			if res.UsedSearch {
				// 扣减一次额度
				r.quota.Consume()
			}

			// token 消耗记录
			totalUsage.Input += usage.Input
			totalUsage.Output += usage.Output
			totalUsage.Think += usage.Think

			// 时间记录
			totalCostTime += res.CostTime

			if res.FunCall != nil && retry == maxStep-2 {
				str := "[System: This is the last step. Please summarize the information you have and give a final response now. DO NOT call any more tools.]"
				safeHistory = append(safeHistory, &genai.Content{
					Role:  "model",
					Parts: []*genai.Part{res.FunCall},
				})
				safeHistory = append(safeHistory, genai.Text(str)...)
			} else if res.FunCall != nil && retry < maxStep-2 {
				fc := res.FunCall
				var toolResponseContent string
				var dict map[string]string
				r.logger.Log("info", "LLM called tool: "+fc.FunctionCall.Name, logger.Options{})

				r.matrix.SendText(bgCtx, rID, "Calling tool: "+fc.FunctionCall.Name)

				switch fc.FunctionCall.Name {
				case "execute_terminal":
					hasPower := slices.Contains(r.cfg.Auth.AdminID, sender)
					if !hasPower {
						toolResponseContent = "[Error: The sender don't have enough power to call this tool.]"
						break
					}
					if cmd, ok := fc.FunctionCall.Args["command"].(string); ok {
						r.logger.Log("info", "Execute Command: "+cmd, logger.Options{})

						r.matrix.SendText(bgCtx, rID, "Execute Command: "+cmd)

						dict = tools.TryExecuteTerminal(cmd, rID, sender)

						toolResponseContent = dict["content"]

						if dict["result"] == "dangerous" {
							str := fmt.Sprintf("**Dangerous command detected**: `%s`\nUsing `/YES [task_id]` or `/NO [task_id]` to determine whether to execute the command.\n`task_id`: `%s`", cmd, dict["task_id"])
							task := tools.Task{RoomID: rID, SenderID: sender, TaskID: dict["task_id"]}
							waitChan := make(chan bool, 1)
							r.pendingApprovals.Store(task, waitChan)
							r.matrix.SendMarkdownWithMath(bgCtx, rID, str)
							var approved bool
							select {
							case approved = <-waitChan:
								if approved {
									dict = tools.ExecuteTerminal(cmd)
									toolResponseContent = dict["content"]
								} else {
									toolResponseContent = "[Error: User refused authorization]"
								}
							case <-time.After(3 * time.Minute): // 设定一个超时时间，防止协程永久泄漏
								approved = false
								r.matrix.SendText(bgCtx, rID, "Authorization expired and has been automatically cancelled: "+dict["task_id"])
								toolResponseContent = "[Error: Authorization expired and has been automatically cancelled.]"
							}
							r.pendingApprovals.Delete(task)
							close(waitChan)
						}
					} else {
						toolResponseContent = "[Error: Invalid command argument]"
					}
				default:
					toolResponseContent = "[Error: Unknown tool]"
				}

				safeHistory = append(safeHistory, &genai.Content{
					Role:  "model",
					Parts: []*genai.Part{fc},
				})

				safeHistory = append(safeHistory, &genai.Content{
					Role: "user",
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								ID:       fc.FunctionCall.ID,
								Name:     fc.FunctionCall.Name,
								Response: map[string]any{"result": toolResponseContent},
							},
						},
					},
				})

				continue
			}
			break
		}
		if err != nil {
			str := "user: " + sender.String() + "\n"
			str += "room: " + rID.String() + "\n"
			str += "request: " + text + "\n"
			str += "time: " + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"

			errMsg := err.Error()

			isLocalTimeout := errors.Is(err, context.DeadlineExceeded)
			isRemoteTimeout := strings.Contains(errMsg, "DEADLINE_EXCEEDED") || strings.Contains(errMsg, "504")
			if isLocalTimeout || isRemoteTimeout {
				str += "LLM call timed out!"
				_ = r.matrix.SendText(bgCtx, rID, "Network congestion.Please try again later.")
				_ = r.logger.Log("error", "Call LLM time out.", logger.Options{})
			} else {
				_ = r.matrix.SendText(bgCtx, rID, "Sorry, I need rest.Please try again later")
				_ = r.logger.Log("error", fmt.Sprintf("Gemini meet an error: %s", err.Error()), logger.Options{})

				str += "error: " + err.Error()
			}

			errs := r.matrix.SendToLogRoom(bgCtx, str)
			for _, err := range errs {
				str := "Sending log to log-room error: " + err.Error()
				_ = r.logger.Log("error", str, logger.Options{})
				r.matrix.SendToLogRoom(ctx, str)
			}
			return
		}

		// 记录日志
		tokenConsume := fmt.Sprintf(
			" | input %d output %d total %d | %v",
			totalUsage.Input,
			totalUsage.Output,
			totalUsage.Think+totalUsage.Input+totalUsage.Output,
			totalCostTime,
		)
		_ = r.logger.Log("bot", text+tokenConsume, logger.Options{UserID: sender.String(), RoomID: rID.String()})

		// 安全地记账
		r.billing.Record(totalUsage.Input, totalUsage.Output, totalUsage.Think)
		if r.billing.CheckAlarm(totalUsage.Input + totalUsage.Output + totalUsage.Think) {
			str := "Dosage Alert!\n"
			str += "user: " + sender.String() + "\n"
			str += "room: " + rID.String() + "\n"
			str += "request: " + text + "\n"
			str += "time: " + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"
			str += tokenConsume
			errs := r.matrix.SendToLogRoom(bgCtx, str)
			for _, err := range errs {
				r.logger.Log("error", "Sending log to log-room error: "+err.Error(), logger.Options{})
			}
		}

		// 确认是否需要执行记忆回传算法
		nowHistoryLen := r.memory.GetHistoryLen(safeHistory)
		needMemoryRetrospection := nowHistoryLen >= r.cfg.Client.MaxMemoryLength && !isGroup
		if needMemoryRetrospection && r.memory.TryLockRoomSummarization(rID) {
			oldH, summarizedPartCount := r.memory.GetOldHistory(safeHistory)
			go r.ExecuteMemoryRetrospection(oldH, summarizedPartCount, rID)
		}

		// 将大模型的纯净回复写入记忆
		if res.CleanParts != nil {
			r.memory.AddModelMsg(rID.String(), isGroup, res.CleanParts)
		}

		// 将富文本渲染并发送到房间
		err = r.matrix.SendMarkdownWithMath(bgCtx, rID, res.RawText)
		if err != nil {
			str := "user: " + sender.String() + "\n"
			str += "room: " + rID.String() + "\n"
			str += "request: " + text + "\n"
			str += "time: " + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"
			str += "error: " + "Failed to send rich message to room: " + err.Error()
			_ = r.matrix.SendText(bgCtx, rID, "sorry, I need rest, please try again later.")
			_ = r.logger.Log("error", "Failed to send rich message to room: "+err.Error(), logger.Options{})
			errs := r.matrix.SendToLogRoom(bgCtx, str)
			for _, err := range errs {
				str := "Sending log to log-room error: " + err.Error()
				_ = r.logger.Log("error", str, logger.Options{})
			}
			return
		}
	}(history, currentCtx.Text, evt.Sender, evt.RoomID, isGroup)
}

// HandleMember 专门处理 m.room.member 事件（原 evtMember.go 的终极进化版）
func (r *Router) HandleMember(ctx context.Context, evt *event.Event) {
	memberEvent := evt.Content.AsMember()
	if memberEvent == nil || evt.StateKey == nil || *evt.StateKey != string(r.cfg.Client.UserID) {
		return
	}

	switch memberEvent.Membership {
	case event.MembershipInvite:
		// 自动同意加入房间
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
		// 被踢出或退出时，物理清除该房间的所有记忆
		r.memory.Delete(evt.RoomID.String())
		_ = r.logger.Log("info", "Auto clear memory of didn't join room: "+evt.RoomID.String(), logger.Options{})
	}
}

package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"nozomi/internal/logger"

	"google.golang.org/genai"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
)

func saveQuota() {
	path := filepath.Join(workdir, "search_quota.json")
	// 使用 json.MarshalIndent 可以让生成的 JSON 文件有缩进
	data, _ := json.MarshalIndent(quota, "", "\t")
	err := os.WriteFile(path, data, 0644)
	if err != nil {
		str := "Saving data to search_quota.json fail." + err.Error()
		logger.Log("error", str, logger.Options{})
		sendToLogRoom(str)
	}
}

func getHistoryLen(history []*genai.Content) int {
	totalLength := 0
	for _, content := range history {
		if content.Role == "user" {
			totalLength += len(content.Parts)
		} else {
			totalLength += 1
		}
	}
	return totalLength
}

func historyCleanup(history []*genai.Content) []*genai.Content {
	totalLength := getHistoryLen(history)
	for totalLength > botConfig.Client.MaxMemoryLength {
		if history[0].Role == "user" && len(history[0].Parts) > 1 {
			history[0].Parts = history[0].Parts[1:]
		} else {
			history = history[1:]
		}
		totalLength--
	}
	for len(history) > 0 && history[0].Role == "model" {
		if len(history) == 1 {
			history = nil
		} else {
			history = history[1:]
		}
	}
	return history
}

func evtMsg(ctx context.Context, evt *event.Event) {
	if evt.Timestamp < bootTimeUnixmilli {
		return
	}

	msg := evt.Content.AsMessage()
	if msg == nil {
		return
	}

	if evt.Sender == client.UserID {
		return
	}

	if msg.MsgType != event.MsgText {
		return
	}

	var req string = msg.Body
	mentionPattern := `\[.*?\]\(https://matrix\.to/#/` + regexp.QuoteMeta(string(client.UserID)) + `\)`
	mentionRegex := regexp.MustCompile(mentionPattern)
	req = mentionRegex.ReplaceAllString(req, "")
	req = strings.ReplaceAll(req, string(client.UserID), "")
	req = strings.ReplaceAll(req, "@[希]", "")
	req = strings.ReplaceAll(req, "@希", "")
	req = strings.ReplaceAll(req, "!c ", "")
	req = strings.TrimSpace(req)
	if len(req) == 0 {
		req = "(呼叫了你(希))"
	}

	// LoadOrStore 保证了即便多个并发同时到达，也只会初始化出一把唯一的锁
	roomLockObj, _ := roomLocks.LoadOrStore(evt.RoomID.String(), &sync.Mutex{})
	roomLock := roomLockObj.(*sync.Mutex)

	roomLock.Lock()
	defer roomLock.Unlock()

	// 取出当前房间的对话记忆
	var history []*genai.Content
	if val, ok := chatMemory.Load(evt.RoomID.String()); ok {
		history = val.([]*genai.Content)
	}

	// 当前的对话内容
	var currentTextPart *genai.Part

	// 房间逻辑
	membersResp, err := client.JoinedMembers(ctx, evt.RoomID)
	if err != nil {
		str := "Failed to get room member list: " + err.Error()
		_ = logger.Log("error", str, logger.Options{})
		sendToLogRoom(str)
		return
	}
	peopleNum := len(membersResp.Joined)
	isGroup := peopleNum > 2
	isMentioned := false
	// 判断是否是群聊
	if isGroup {
		// 群聊逻辑
		if strings.HasPrefix(msg.Body, "!c ") {
			isMentioned = true
		}
		if strings.HasPrefix(msg.Body, string(client.UserID)) {
			isMentioned = true
		}
		if msg.Mentions != nil && len(msg.Mentions.UserIDs) > 0 {
			for _, uid := range msg.Mentions.UserIDs {
				if uid == client.UserID {
					isMentioned = true
					break
				}
			}
		}
		str := fmt.Sprintf("%s 发言：%s\n", evt.Sender.String(), req)
		currentTextPart = genai.Text(str)[0].Parts[0]
	} else {
		// 私信逻辑
		isMentioned = true
		currentTextPart = genai.Text(req)[0].Parts[0]
	}
	// 防止 Gemini 连续 user 报错：合并同类项
	historyLen := len(history)
	if historyLen > 0 && history[historyLen-1].Role == "user" {
		// 上一句话是人类说的（只有群聊会出现），直接把新的文本 Part 塞进上一个 user 的包裹里
		history[historyLen-1].Parts = append(history[historyLen-1].Parts, currentTextPart)
	} else {
		// 上一句话是大模型的，这是一个全新的对话，创建一个新的 user 节点
		userMsg := &genai.Content{
			Role:  "user",
			Parts: []*genai.Part{currentTextPart},
		}
		history = append(history, userMsg)
	}
	history = historyCleanup(history)
	chatMemory.Store(evt.RoomID.String(), history)

	if !isMentioned {
		return
	}

	// 深拷贝一份绝对干净的历史记录给大模型，防止指针并发崩溃
	sendHistory := make([]*genai.Content, len(history))
	for i, h := range history {
		partsCopy := make([]*genai.Part, len(h.Parts))
		copy(partsCopy, h.Parts)
		sendHistory[i] = &genai.Content{
			Role:  h.Role,
			Parts: partsCopy,
		}
	}

	go func(evt *event.Event, req string, history []*genai.Content) {
		// 由于是独立协程，不允许再使用外部context
		ctx := context.Background()

		reqConfig := botConfig.Model.Config
		nowMonth := time.Now().Format("2006-01")
		searchMutex.Lock()
		if quota.Month != nowMonth {
			quota.Month = nowMonth
			quota.Count = botConfig.Model.MaxMonthlySearch
			saveQuota()
		}
		if quota.Count <= 0 {
			tempConfig := *reqConfig //解引用拿到浅拷贝
			tempConfig.Tools = nil
			reqConfig = &tempConfig
		}
		searchMutex.Unlock()

		// 调用大模型
		result, costTime, err := Call(history, reqConfig)
		if err != nil {
			str := "用户：" + evt.Sender.String() + "\n"
			str += "房间：" + evt.RoomID.String() + "\n"
			str += "请求：" + req + "\n"
			str += "时间：" + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"

			errMsg := err.Error()
			isLocalTimeout := errors.Is(err, context.DeadlineExceeded)
			isRemoteTimeout := strings.Contains(errMsg, "DEADLINE_EXCEEDED") || strings.Contains(errMsg, "504")
			if isLocalTimeout || isRemoteTimeout {
				str += fmt.Sprintf("大模型调用超时：%v", costTime)
				_, _ = client.SendText(ctx, evt.RoomID, "Network congestion.Please try again later.")
				_ = logger.Log("error", fmt.Sprintf("Call LLM time out, spent: %v", costTime), logger.Options{})
			} else {
				_, _ = client.SendText(ctx, evt.RoomID, "Sorry, I need rest.")
				_ = logger.Log("error", fmt.Sprintf("Gemini meet a error: %s", err.Error()), logger.Options{})

				str += "错误：" + err.Error()
			}

			sendToLogRoom(str)
			return
		}

		tokenConsume := fmt.Sprintf(
			" | 输入%d 输出%d 总计消耗%d | %v",
			result.UsageMetadata.PromptTokenCount,
			result.UsageMetadata.CandidatesTokenCount,
			result.UsageMetadata.TotalTokenCount,
			costTime,
		)
		_ = logger.Log("bot", req+tokenConsume, logger.Options{UserID: evt.Sender.String(), RoomID: evt.RoomID.String()})

		if result.UsageMetadata.TotalTokenCount >= botConfig.Model.AlargmTokenCount {
			tokenConsume = strings.TrimPrefix(tokenConsume, " | ")
			str := "用量警报！\n"
			str += "用户：" + evt.Sender.String() + "\n"
			str += "房间：" + evt.RoomID.String() + "\n"
			str += "请求：" + req + "\n"
			str += "时间: " + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05") + "\n"
			str += "Token 账单单次达到警报值！\n"
			str += tokenConsume
			sendToLogRoom(str)
		}

		if len(result.Candidates) > 0 && result.Candidates[0].GroundingMetadata != nil {
			// 只要 GroundingMetadata 不为空，且包含了搜索入口或数据块，就说明大模型悄悄上网了
			meta := result.Candidates[0].GroundingMetadata
			if meta.SearchEntryPoint != nil || len(meta.GroundingChunks) > 0 {
				// fmt.Println("大模型搜索了:", meta.WebSearchQueries)
				searchMutex.Lock()
				quota.Count--
				saveQuota()
				searchMutex.Unlock()
			}
		}

		raw := result.Text()
		raw = strings.TrimSpace(raw)
		re := regexp.MustCompile(`\n{3,}`)
		raw = re.ReplaceAllString(raw, "\n\n")

		if len(result.Candidates) > 0 && result.Candidates[0].Content != nil {
			safeModelMsg := result.Candidates[0].Content
			safeModelMsg.Role = "model"

			if len(safeModelMsg.Parts) > 0 {
				roomLock.Lock()
				if val, ok := chatMemory.Load(evt.RoomID.String()); ok {
					latestHistory := val.([]*genai.Content)
					latestHistory = append(latestHistory, safeModelMsg)
					latestHistory = historyCleanup(latestHistory)
					chatMemory.Store(evt.RoomID.String(), latestHistory)
				}
				roomLock.Unlock()
			} else {
				str := "The model return null value and has been inhibit.Question: " + req
				_ = logger.Log("info", str, logger.Options{})
				_, _ = client.SendText(ctx, evt.RoomID, "Sorry, I wan't to answer this question.")

				str = "模型返回空警报！\n"
				str += "用户：" + evt.Sender.String() + "\n"
				str += "房间：" + evt.RoomID.String() + "\n"
				str += "请求：" + req + "\n"
				str += "时间: " + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05")
				sendToLogRoom(str)
				return
			}
		}

		richMsg := format.RenderMarkdown(raw, true, false)
		parts := strings.Split(richMsg.FormattedBody, "<pre>")
		for i := range parts {
			if i == 0 {
				parts[i] = strings.ReplaceAll(parts[i], "\n", "")
			} else {
				subParts := strings.SplitN(parts[i], "</pre>", 2)
				if len(subParts) == 2 {
					subParts[1] = strings.ReplaceAll(subParts[1], "\n", "")
					parts[i] = subParts[0] + "</pre>" + subParts[1]
				}
			}
		}
		richMsg.FormattedBody = strings.Join(parts, "<pre>")

		_, err = client.SendMessageEvent(ctx, evt.RoomID, event.EventMessage, &richMsg)
		if err != nil {
			_ = logger.Log("error", "Failed to send matrix rich message: "+err.Error(), logger.Options{})
			str := "Failed to send matrix rich message.\n"
			str += "用户：" + evt.Sender.String() + "\n"
			str += "房间：" + evt.RoomID.String() + "\n"
			str += "请求：" + req + "\n"
			str += "时间：" + time.UnixMilli(evt.Timestamp).Format("2006-01-02 15:04:05")
			sendToLogRoom(str)
		}
	}(evt, req, sendHistory)
}

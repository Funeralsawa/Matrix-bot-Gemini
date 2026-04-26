package handler

import (
	"context"
	"fmt"
	"nozomi/internal/llm"
	"nozomi/internal/logger"
	"nozomi/internal/matrix"
	"nozomi/tools"
	"strings"
	"time"

	"google.golang.org/genai"
	"maunium.net/go/mautrix/id"
)

// 将多维度的历史记录与多张图片降维拼装为多模态剧本
func (r *Router) FormatMessageContexts(msgCtxs []*matrix.MessageContext, isGroup bool) (finalText string, finalImages []*genai.Part) {
	var combinedTextBuilder strings.Builder
	finalImages = make([]*genai.Part, 0)

	for i, mCtx := range msgCtxs {
		if mCtx.ImagePart != nil {
			tag := fmt.Sprintf("\n[The following are the file names corresponding to visual attachments：%s]\n", mCtx.FileName)
			finalImages = append(finalImages, &genai.Part{Text: tag})
			finalImages = append(finalImages, mCtx.ImagePart)
		}
		nodeTime := time.UnixMilli(mCtx.EventTime).Format("2006/01/02 15:04")
		if len(msgCtxs) > 1 {
			if i < len(msgCtxs)-1 {
				combinedTextBuilder.WriteString(fmt.Sprintf("【Historical citation hierarchy %d】[%s] %s say：%s\n", i+1, nodeTime, mCtx.Sender, mCtx.Text))
			} else {
				if isGroup {
					combinedTextBuilder.WriteString(fmt.Sprintf("【Latest comments】[%s] %s say：%s\n", nodeTime, mCtx.Sender, mCtx.Text))
				} else {
					combinedTextBuilder.WriteString(fmt.Sprintf("【Latest comments】[%s] %s\n", nodeTime, mCtx.Text))
				}
			}
		} else {
			if isGroup {
				combinedTextBuilder.WriteString(fmt.Sprintf("[%s] %s say：%s\n", nodeTime, mCtx.Sender, mCtx.Text))
			} else {
				combinedTextBuilder.WriteString(fmt.Sprintf("[%s] %s\n", nodeTime, mCtx.Text))
			}
		}
	}

	finalText = strings.TrimSpace(combinedTextBuilder.String())

	return finalText, finalImages
}

func (r *Router) ExecuteMemoryRetrospection(oldH []*genai.Content, sumCount int, roomID id.RoomID) {
	defer r.memory.UnlockRoomSummarization(roomID)
	bgCtx := context.Background()
	str := "Briefly summarize the content of this chat log in no more than 300 words."
	var dynamicConfig *genai.GenerateContentConfig
	cfgCopy := *r.cfg.Model.Config
	dynamicConfig = &cfgCopy // 深拷贝取新地址
	if r.quota.CheckAndGetRemaining() <= 0 {
		dynamicConfig = r.llm.GetConfigWithoutSearch()
	}
	dynamicConfig.SystemInstruction = genai.Text(str)[0]
	var res *llm.GenerateResult
	var usage *llm.TokenUsage
	var err error
	for retry := 0; retry < 3; retry++ {
		res, usage, err = r.llm.Generate(bgCtx, oldH, dynamicConfig)
		if err != nil && retry < 3 {
			time.Sleep(time.Second * 2)
			continue
		} else if err != nil && retry >= 3 {
			r.memory.ForceCleanupAndStore(roomID.String())
			str := fmt.Sprintf("Execute memory retrospection failed for room %s", roomID.String())
			r.logger.Log("error", str, logger.Options{})
			return
		}
		break
	}
	if res.UsedSearch {
		r.quota.Consume()
	}
	r.billing.Record(usage.Input, usage.Output, usage.Think)
	err = r.memory.MemoryRetrospection(roomID, res.CleanParts, sumCount)
	if err != nil {
		str := fmt.Sprintf("Memort Retrospection for room %s failed: %v", roomID.String(), err)
		r.logger.Log("error", str, logger.Options{})
	}
}

func (r *Router) checkIsPendingTask(ctx context.Context, text string, roomID id.RoomID, sender id.UserID) bool {
	if strings.HasPrefix(text, "/YES ") {
		taskID := strings.TrimSpace(strings.TrimPrefix(text, "/YES "))

		// 查找是否有这个 task_id 在等待
		if ch, ok := r.pendingApprovals.Load(tools.Task{RoomID: roomID, SenderID: sender, TaskID: taskID}); ok {
			waitChan := ch.(chan bool)
			r.matrix.SendText(ctx, roomID, "Authorized task: "+taskID)
			waitChan <- true // 发送放行信号，唤醒等待的协程
		} else {
			r.matrix.SendText(ctx, roomID, "Invalid or expired task ID")
		}
		return true
	}

	if strings.HasPrefix(text, "/NO ") {
		taskID := strings.TrimSpace(strings.TrimPrefix(text, "/NO "))
		if ch, ok := r.pendingApprovals.Load(tools.Task{RoomID: roomID, SenderID: sender, TaskID: taskID}); ok {
			waitChan := ch.(chan bool)
			r.matrix.SendText(ctx, roomID, "Task rejected: "+taskID)
			waitChan <- false // 发送拒绝信号
		}
		return true
	}

	return false
}

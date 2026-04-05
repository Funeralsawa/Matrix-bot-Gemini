package handler

import (
	"fmt"
	"nozomi/internal/matrix"
	"strings"
	"time"

	"google.golang.org/genai"
)

// 将多维度的历史记录与多张图片降维拼装为多模态剧本
func (r *Router) FormatMessageContexts(msgCtxs []*matrix.MessageContext, isGroup bool) (finalText string, finalImages []*genai.Part) {
	var combinedTextBuilder strings.Builder
	finalImages = make([]*genai.Part, 0)

	for i, mCtx := range msgCtxs {
		if mCtx.ImagePart != nil {
			tag := fmt.Sprintf("\n[以下视觉附件对应文件名：%s]\n", mCtx.FileName)
			finalImages = append(finalImages, &genai.Part{Text: tag})
			finalImages = append(finalImages, mCtx.ImagePart)
		}
		nodeTime := time.UnixMilli(mCtx.EventTime).Format("2006/01/02 15:04")
		if len(msgCtxs) > 1 {
			if i < len(msgCtxs)-1 {
				combinedTextBuilder.WriteString(fmt.Sprintf("【历史引用层级 %d】[%s] %s 发言：%s\n", i+1, nodeTime, mCtx.Sender, mCtx.Text))
			} else {
				if isGroup {
					combinedTextBuilder.WriteString(fmt.Sprintf("【当前最新发言】[%s] %s 发言：%s\n", nodeTime, mCtx.Sender, mCtx.Text))
				} else {
					combinedTextBuilder.WriteString(fmt.Sprintf("【当前最新发言】[%s] %s\n", nodeTime, mCtx.Text))
				}
			}
		} else {
			if isGroup {
				combinedTextBuilder.WriteString(fmt.Sprintf("[%s] %s 发言：%s\n", nodeTime, mCtx.Sender, mCtx.Text))
			} else {
				combinedTextBuilder.WriteString(fmt.Sprintf("[%s] %s\n", nodeTime, mCtx.Text))
			}
		}
	}

	finalText = strings.TrimSpace(combinedTextBuilder.String())

	return finalText, finalImages
}

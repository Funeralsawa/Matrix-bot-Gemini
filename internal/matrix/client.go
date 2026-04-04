package matrix

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"nozomi/internal/config"

	"google.golang.org/genai"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

// MessageContext 结构化解析后的消息，供 handler 直接调度
type MessageContext struct {
	Text        string      // 清洗后的纯文本
	ImagePart   *genai.Part // 解密后的图片数据（如果有）
	IsMentioned bool        // 是否被提及
}

type Client struct {
	client *mautrix.Client
	crypto *cryptohelper.CryptoHelper
	cfg    *config.BotConfig
}

// NewClient 封装初始化与加密模块启动逻辑
func NewClient(cfg *config.BotConfig) (*Client, error) {
	c, err := mautrix.NewClient(cfg.Client.HomeserverURL, cfg.Client.UserID, cfg.Client.AccessToken)
	if err != nil {
		return nil, err
	}
	c.DeviceID = id.DeviceID(cfg.Client.DeviceID)

	// 初始化加密数据库
	dbPath := filepath.Join(cfg.WorkDir, "database", "bot_crypto.db")
	crypto, err := cryptohelper.NewCryptoHelper(c, []byte(cfg.Client.DatabasePassword), dbPath)
	if err != nil {
		return nil, err
	}

	err = crypto.Init(context.Background())
	if err != nil {
		return nil, err
	}
	c.Crypto = crypto

	return &Client{
		client: c,
		crypto: crypto,
		cfg:    cfg,
	}, nil
}

// Sync 启动长连接同步
func (c *Client) Sync(ctx context.Context) error {
	return c.client.Sync()
}

// OnEvent 开放底层 Syncer 的事件注册
func (c *Client) OnEvent(evtType event.Type, handler func(context.Context, *event.Event)) {
	syncer, ok := c.client.Syncer.(*mautrix.DefaultSyncer)
	if ok {
		syncer.OnEventType(evtType, handler)
	}
}

// ParseMessage 核心：处理解密、嵌套剥离、多模态提取
func (c *Client) ParseMessage(ctx context.Context, evt *event.Event) (*MessageContext, error) {
	msg := evt.Content.AsMessage()
	if msg == nil {
		return nil, fmt.Errorf("not a message event")
	}

	isSticker := evt.Type == event.EventSticker

	if !isSticker && msg.MsgType != event.MsgText && msg.MsgType != event.MsgImage {
		return nil, fmt.Errorf("not a message event")
	}

	res := &MessageContext{}
	req := msg.Body

	// 1. 处理引用与嵌套
	quote, reply := c.extractReply(req)
	if quote != "" {
		req = fmt.Sprintf("(引用回复了：“%s”)\n%s", quote, reply)
	} else if msg.RelatesTo != nil && msg.RelatesTo.InReplyTo != nil {
		// 处理 MSC2802 原生回复
		e, err := c.fetchAndDecryptRemoteEvent(ctx, evt.RoomID, msg.RelatesTo.InReplyTo.EventID)
		if err != nil {
			return nil, err
		}
		m := e.Content.AsMessage()
		if m != nil {
			_, pureRemoteText := c.extractReply(m.Body) //剥离历史信息本身的引用
			if pureRemoteText != "" {
				quote = pureRemoteText
				reply = req
				req = fmt.Sprintf("(引用回复了：“%s”)\n%s", quote, req)
				res.IsMentioned = e.Sender == c.client.UserID
			}
		}
	}

	// 2. 处理图片与解密
	if msg.MsgType == event.MsgImage || isSticker {
		imgData, mime := c.downloadAndDecryptImage(ctx, msg)
		if len(imgData) > 0 {
			compressedImg, finalMime, _ := c.CompressImageTo720p(imgData)
			imgData = compressedImg
			mime = finalMime
			res.ImagePart = &genai.Part{
				InlineData: &genai.Blob{MIMEType: mime, Data: imgData},
			}
			if isSticker {
				req = "(发送了一张贴纸)"
			} else {
				req = c.sniffImageCaption(req) // 嗅探并清理常规图片文件名占位符
			}
		}
	}

	// 3. 艾特判定与清理
	if !res.IsMentioned {
		res.IsMentioned = c.checkMention(evt, quote, reply)
	}
	res.Text = c.cleanMentionAndCmd(req)

	return res, nil
}

// SendMarkdown 封装 HTML 渲染与格式清洗
func (c *Client) SendMarkdown(ctx context.Context, roomID id.RoomID, rawText string) error {
	richMsg := format.RenderMarkdown(rawText, true, false)

	// 清理 <pre> 标签内的多余换行
	parts := strings.Split(richMsg.FormattedBody, "<pre>")
	for i := range parts {
		if i > 0 {
			sub := strings.SplitN(parts[i], "</pre>", 2)
			if len(sub) == 2 {
				sub[1] = strings.ReplaceAll(sub[1], "\n", "")
				parts[i] = sub[0] + "</pre>" + sub[1]
			}
		} else {
			parts[i] = strings.ReplaceAll(parts[i], "\n", "")
		}
	}
	richMsg.FormattedBody = strings.Join(parts, "<pre>")

	_, err := c.client.SendMessageEvent(ctx, roomID, event.EventMessage, &richMsg)
	return err
}

// GetJoinedRooms 获取当前机器人加入的所有房间 ID
func (c *Client) GetJoinedRooms(ctx context.Context) ([]string, error) {
	resp, err := c.client.JoinedRooms(ctx)
	if err != nil {
		return nil, err
	}
	var rooms []string
	for _, r := range resp.JoinedRooms {
		rooms = append(rooms, r.String())
	}
	return rooms, nil
}

// GetRoomMemberCount 获取指定房间的人数
func (c *Client) GetRoomMemberCount(ctx context.Context, roomID string) (int, error) {
	resp, err := c.client.JoinedMembers(ctx, id.RoomID(roomID))
	if err != nil {
		return 0, err
	}
	return len(resp.Joined), nil
}

// LeaveRoom 退出房间
func (c *Client) LeaveRoom(ctx context.Context, roomID string) error {
	_, err := c.client.LeaveRoom(ctx, id.RoomID(roomID))
	return err
}

// 发送一条纯文本信息
func (c *Client) SendText(ctx context.Context, roomID id.RoomID, text string) error {
	_, err := c.client.SendText(ctx, roomID, text)
	return err
}

// 向日志房间发送报告
func (c *Client) SendToLogRoom(ctx context.Context, text string) []error {
	var errs []error
	for _, room := range c.cfg.Client.LogRoom {
		targetRoom := id.RoomID(room)
		if err := c.SendText(ctx, targetRoom, text); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (c *Client) JoinRoom(ctx context.Context, roomID id.RoomID) error {
	_, err := c.client.JoinRoomByID(ctx, roomID)
	return err
}

// 传统引用回复 Fallback 至 msg.body，格式化嵌套引用
func (c *Client) extractReply(raw string) (string, string) {
	if !strings.HasPrefix(raw, ">") {
		return "", raw
	}
	lines := strings.Split(raw, "\n")
	var quotes, replies []string
	inQuote := true
	for _, l := range lines {
		if inQuote && strings.HasPrefix(l, ">") {
			quotes = append(quotes, strings.TrimLeft(l, "> "))
		} else if inQuote && strings.TrimSpace(l) == "" {
			inQuote = false
		} else {
			inQuote = false
			replies = append(replies, l)
		}
	}
	return strings.Join(quotes, "\n"), strings.Join(replies, "\n")
}

func (c *Client) downloadAndDecryptImage(ctx context.Context, msg *event.MessageEventContent) ([]byte, string) {
	var data []byte
	var mime string
	if msg.File != nil { // 加密图片
		uri, _ := msg.File.URL.Parse()
		encData, _ := c.client.DownloadBytes(ctx, uri)
		_ = msg.File.DecryptInPlace(encData)
		data = encData
		mime = msg.Info.MimeType
	} else if len(msg.URL) > 0 { // 明文图片
		uri, _ := msg.URL.Parse()
		data, _ = c.client.DownloadBytes(ctx, uri)
		mime = msg.Info.MimeType
	}
	return data, mime
}

func (c *Client) fetchAndDecryptRemoteEvent(ctx context.Context, roomID id.RoomID, eventID id.EventID) (*event.Event, error) {
	evt, err := c.client.GetEvent(ctx, roomID, eventID)
	if err != nil {
		return nil, err
	}
	if evt.Type == event.EventEncrypted && c.crypto != nil {
		err = evt.Content.ParseRaw(evt.Type) // 将 Raw JSON 反序列化为底层解密引擎需要的结构体
		if err != nil && !errors.Is(err, event.ErrContentAlreadyParsed) {
			return nil, err
		}
		dec, err := c.crypto.Decrypt(ctx, evt)
		if err != nil {
			return nil, err
		}
		evt = dec
	}
	return evt, nil
}

func (c *Client) checkMention(evt *event.Event, quote, reply string) bool {
	if strings.Contains(reply, string(c.client.UserID)) || strings.Contains(reply, "!c") {
		return true
	}
	regexEngine := regexp.MustCompile(`<([^>]+)>`)
	matches := regexEngine.FindStringSubmatch(quote)
	if len(matches) > 1 {
		// matches[0] 是 "<@bot:sigh.work>"
		// matches[1] 是 "@bot:sigh.work"
		return matches[1] == c.client.UserID.String()
	}
	msg := evt.Content.AsMessage()
	if msg.Mentions != nil {
		for _, uid := range msg.Mentions.UserIDs {
			if uid == c.client.UserID {
				return true
			}
		}
	}
	return false
}

func (c *Client) cleanMentionAndCmd(raw string) string {
	mentionPattern := `\[.*?\]\(https://matrix\.to/#/` + regexp.QuoteMeta(string(c.client.UserID)) + `\)`
	re := regexp.MustCompile(mentionPattern)
	res := re.ReplaceAllString(raw, "")
	res = strings.ReplaceAll(res, "!c ", "")
	return strings.TrimSpace(res)
}

func (c *Client) sniffImageCaption(req string) string {
	lower := strings.ToLower(req)
	isFilename := (strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpeg")) && !strings.Contains(req, " ")
	if len(req) == 0 || isFilename {
		return "(发送了一张图片)"
	}
	return "(发送了一张图片并配文) " + req
}

// 资料自愈支持
func (c *Client) GetProfile() (string, string, error) {
	name, err := c.client.GetDisplayName(context.Background(), c.client.UserID)
	if err != nil {
		return "", "", err
	}
	avatar, err := c.client.GetAvatarURL(context.Background(), c.client.UserID)
	if err != nil {
		return "", "", err
	}
	return name.DisplayName, avatar.String(), nil
}

func (c *Client) SetProfile(name, avatar string) error {
	if name != "" {
		err := c.client.SetDisplayName(context.Background(), name)
		if err != nil {
			return err
		}
	}
	if avatar != "" {
		uri, err := id.ParseContentURI(avatar)
		if err != nil {
			return err
		}
		_ = c.client.SetAvatarURL(context.Background(), uri)
	}
	return nil
}

func (c *Client) Close() {
	if c.crypto != nil {
		c.crypto.Close()
	}
}

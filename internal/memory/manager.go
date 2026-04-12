package memory

import (
	"fmt"
	"sync"
	"time"

	"google.golang.org/genai"
	"maunium.net/go/mautrix/id"
)

type ImageCacheItem struct {
	sync.Mutex
	Parts      []*genai.Part
	ExpireTime time.Time
}

type Manager struct {
	chatMemory            sync.Map // 格式: map[string][]*genai.Content
	privateImageCache     sync.Map // 格式: map[string]*ImageCacheItem
	summarizingRooms      sync.Map // 格式: map[string]struct{}
	roomLocks             sync.Map // 格式: map[string]*sync.Mutex
	maxMemoryLength       int      // 记忆最大长度（图纸配置）
	whenRetroRemainMemLen int      // 记忆回传算法执行时留下的记忆长度
}

// 构造函数
func NewManager(maxLength int, maxRemainLen int) *Manager {
	return &Manager{
		maxMemoryLength:       maxLength,
		whenRetroRemainMemLen: maxRemainLen,
	}
}

// 获取房间锁，安全控制并发
func (m *Manager) getRoomLock(roomID string) *sync.Mutex {
	lockObj, _ := m.roomLocks.LoadOrStore(roomID, &sync.Mutex{})
	return lockObj.(*sync.Mutex)
}

// Load 取出当前房间的对话记忆（深拷贝，防止指针踩踏）
func (m *Manager) Load(roomID string) []*genai.Content {
	m.getRoomLock(roomID).Lock()
	defer m.getRoomLock(roomID).Unlock()

	var history []*genai.Content
	if val, ok := m.chatMemory.Load(roomID); ok {
		rawHistory := val.([]*genai.Content)
		// 深拷贝逻辑
		history = make([]*genai.Content, len(rawHistory))
		for i, h := range rawHistory {
			partsCopy := make([]*genai.Part, len(h.Parts))
			copy(partsCopy, h.Parts)
			history[i] = &genai.Content{
				Role:  h.Role,
				Parts: partsCopy,
			}
		}
	}
	return history
}

// AddUserMsgAndLoad 记录群友的新发言，并返回用于大模型调用的深拷贝记忆
func (m *Manager) AddUserMsgAndLoad(roomID string, isGroup bool, text string, imgPart ...*genai.Part) []*genai.Content {
	m.getRoomLock(roomID).Lock()
	defer m.getRoomLock(roomID).Unlock()

	var history []*genai.Content
	if val, ok := m.chatMemory.Load(roomID); ok {
		history = val.([]*genai.Content)
	}

	// 组装当前这句发言的 Parts
	var currentParts []*genai.Part
	cachedImgs := m.pullPrivateImageCache(roomID)
	if len(cachedImgs) > 0 {
		label := "发送了一组图片："
		currentParts = append(currentParts, genai.Text(label)[0].Parts[0])
		currentParts = append(currentParts, cachedImgs...)
	}
	if text != "" {
		currentParts = append(currentParts, genai.Text(text)[0].Parts[0])
	}
	if imgPart != nil {
		currentParts = append(currentParts, imgPart...)
	}

	if len(currentParts) == 0 {
		return m.deepCopy(history)
	}

	// 防止 Gemini 连续 user 报错：合并同类项
	historyLen := len(history)
	if historyLen > 0 && history[historyLen-1].Role == "user" {
		// 上一句话是人类说的，直接把新的文本塞进上一个 user 的包裹里
		history[historyLen-1].Parts = append(history[historyLen-1].Parts, currentParts...)
	} else {
		// 上一句话是大模型的，这是一个全新的对话，创建一个新的 user 节点
		userMsg := &genai.Content{
			Role:  "user",
			Parts: currentParts,
		}
		history = append(history, userMsg)
	}

	// 裁切并保存到原始内存
	if isGroup {
		history = m.cleanup(roomID, history)
	}
	m.chatMemory.Store(roomID, history)

	// 返回一份深拷贝，防止指针被并发踩踏
	return m.deepCopy(history)
}

// AddModelMsg 将大模型的纯净回复写入记忆
func (m *Manager) AddModelMsg(roomID string, isGroup bool, cleanParts []*genai.Part) {
	if len(cleanParts) == 0 {
		return
	}

	m.getRoomLock(roomID).Lock()
	defer m.getRoomLock(roomID).Unlock()

	var history []*genai.Content
	if val, ok := m.chatMemory.Load(roomID); ok {
		history = val.([]*genai.Content)
	}

	safeModelMsg := &genai.Content{
		Role:  "model",
		Parts: cleanParts,
	}

	history = append(history, safeModelMsg)
	if isGroup {
		history = m.cleanup(roomID, history)
	}
	m.chatMemory.Store(roomID, history)
}

// CleanExpiredImages 扫描并清理过期的私聊图片缓存，返回已清理的 roomID 列表供外层通知
func (m *Manager) CleanExpiredImages() []string {
	var expiredRooms []string
	now := time.Now()

	m.privateImageCache.Range(func(key, val any) bool {
		roomID := key.(string)
		cache := val.(*ImageCacheItem)

		cache.Lock()
		isExpired := now.After(cache.ExpireTime)
		cache.Unlock()

		if isExpired {
			m.privateImageCache.Delete(roomID)
			expiredRooms = append(expiredRooms, roomID)
		}
		return true
	})
	return expiredRooms
}

// RetainOnly 对比活跃房间列表，把不在列表里的幽灵房间记忆抹除
func (m *Manager) RetainOnly(activeRooms []string) {
	validRooms := make(map[string]bool)
	for _, r := range activeRooms {
		validRooms[r] = true
	}

	m.chatMemory.Range(func(key, val any) bool {
		roomID := key.(string)
		if !validRooms[roomID] {
			m.chatMemory.Delete(key)
			m.privateImageCache.Delete(key)
		}
		return true
	})
}

// AddPrivateImageCache 专门用于私聊：暂存没有配文的图片，并返回当前已暂存的数量
func (m *Manager) AddPrivateImageCache(roomID string, imgPart *genai.Part) int {
	val, _ := m.privateImageCache.LoadOrStore(roomID, &ImageCacheItem{})
	cache := val.(*ImageCacheItem)

	cache.Lock()
	defer cache.Unlock()

	// 追加新图之前先判断是否有过期旧图缓存
	if time.Now().After(cache.ExpireTime) && len(cache.Parts) > 0 {
		cache.Parts = nil
	}
	cache.Parts = append(cache.Parts, imgPart)
	cache.ExpireTime = time.Now().Add(5 * time.Minute)

	return len(cache.Parts)
}

// pullPrivateImageCache 取出并清空暂存的私聊图片，准备与文字合并
func (m *Manager) pullPrivateImageCache(roomID string) []*genai.Part {
	val, ok := m.privateImageCache.Load(roomID)
	if !ok {
		return nil
	}

	cache := val.(*ImageCacheItem)
	cache.Lock()
	defer cache.Unlock()

	var parts []*genai.Part
	if time.Now().Before(cache.ExpireTime) && len(cache.Parts) > 0 {
		parts = append(parts, cache.Parts...)
	}
	cache.Parts = nil // 取出后立刻清空
	m.privateImageCache.Delete(roomID)

	return parts
}

// Store 将清理过的最新记忆安全地写回内存
func (m *Manager) Store(roomID string, history []*genai.Content) {
	m.getRoomLock(roomID).Lock()
	defer m.getRoomLock(roomID).Unlock()

	// 调用内部的私有滑动窗口清理函数
	cleanHistory := m.cleanup(roomID, history)
	m.chatMemory.Store(roomID, cleanHistory)
}

// 清除指定房间的记忆 (比如退群或空房间时调用)
func (m *Manager) Delete(roomID string) {
	m.chatMemory.Delete(roomID)
	m.privateImageCache.Delete(roomID)
}

// deepCopy 深拷贝工具
func (m *Manager) deepCopy(rawHistory []*genai.Content) []*genai.Content {
	history := make([]*genai.Content, len(rawHistory))
	for i, h := range rawHistory {
		partsCopy := make([]*genai.Part, len(h.Parts))
		copy(partsCopy, h.Parts)
		history[i] = &genai.Content{
			Role:  h.Role,
			Parts: partsCopy,
		}
	}
	return history
}

func (m *Manager) TryLockRoomSummarization(roomID id.RoomID) bool {
	_, loaded := m.summarizingRooms.LoadOrStore(roomID.String(), struct{}{})
	return !loaded
}

func (m *Manager) UnlockRoomSummarization(roomID id.RoomID) {
	m.summarizingRooms.Delete(roomID.String())
}

// 记忆总结回传算法
func (m *Manager) MemoryRetrospection(roomID id.RoomID, conclusion []*genai.Part, summarizedPartCount int) error {
	m.getRoomLock(roomID.String()).Lock()
	defer m.getRoomLock(roomID.String()).Unlock()

	h, ok := m.chatMemory.Load(roomID.String())
	if !ok {
		return fmt.Errorf("This room has no memory record.")
	}
	nowHistory := h.([]*genai.Content)

	if summarizedPartCount <= 0 {
		return nil
	}

	var newHistory []*genai.Content
	newHistory = append(newHistory, &genai.Content{
		Role:  "user",
		Parts: conclusion,
	})

	k := 0
	for i := 0; i < len(nowHistory); i++ {
		if nowHistory[i].Role == "model" {
			if k >= summarizedPartCount {
				newHistory = m.appendContentSafely(newHistory, nowHistory[i])
			}
			k++
			continue
		}

		content := &genai.Content{Role: "user"}
		for j := 0; j < len(nowHistory[i].Parts); j++ {
			if k >= summarizedPartCount {
				content.Parts = append(content.Parts, nowHistory[i].Parts[j])
			}
			k++
		}

		if len(content.Parts) > 0 {
			newHistory = m.appendContentSafely(newHistory, content)
		}
	}

	m.chatMemory.Store(roomID.String(), newHistory)
	return nil
}

func (m *Manager) appendContentSafely(history []*genai.Content, newContent *genai.Content) (mergedHistory []*genai.Content) {
	if len(history) == 0 {
		return append(history, newContent)
	}
	lastIndex := len(history) - 1
	if history[lastIndex].Role == newContent.Role {
		history[lastIndex].Parts = append(history[lastIndex].Parts, newContent.Parts...)
		return history
	}
	return append(history, newContent)
}

// 获取旧记忆，返回旧记忆与记忆数量
func (m *Manager) GetOldHistory(history []*genai.Content) ([]*genai.Content, int) {
	totalLen := m.GetHistoryLen(history)
	if totalLen <= m.whenRetroRemainMemLen {
		return nil, 0
	}
	var h []*genai.Content
	contentlLen := len(history)
	targetLen := totalLen - m.whenRetroRemainMemLen
	if targetLen <= 0 {
		return nil, 0
	}
	k := 0
	for i := 0; i < contentlLen && k < targetLen; i++ {
		if history[i].Role == "model" {
			h = append(h, history[i])
			k++
			continue
		}
		content := &genai.Content{
			Role: "user",
		}
		for j := 0; j < len(history[i].Parts) && k < targetLen; j++ {
			content.Parts = append(content.Parts, history[i].Parts[j])
			k++
		}
		if len(content.Parts) > 0 {
			h = append(h, content)
		}
	}
	return h, k
}

// 获取记忆长度
func (m *Manager) GetHistoryLen(history []*genai.Content) int {
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

func (m *Manager) ForceCleanupAndStore(roomID string) {
	m.getRoomLock(roomID).Lock()
	defer m.getRoomLock(roomID).Unlock()

	if val, ok := m.chatMemory.Load(roomID); ok {
		history := val.([]*genai.Content)
		history = m.purgeSlidingWindow(history)
		m.chatMemory.Store(roomID, history)
	}
}

// 滑动窗口裁剪逻辑
func (m *Manager) cleanup(roomID string, history []*genai.Content) []*genai.Content {
	if _, summarizing := m.summarizingRooms.Load(roomID); summarizing {
		return history
	}
	return m.purgeSlidingWindow(history)
}

func (m *Manager) purgeSlidingWindow(history []*genai.Content) []*genai.Content {
	totalLength := m.GetHistoryLen(history)
	for totalLength > m.maxMemoryLength {
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
